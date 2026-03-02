package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"my-movie-site/models"
)

const defaultMaxWorkers = 5

// apiMovie maps the JSON fields returned by the remote API.
type apiMovie struct {
	VodID      int    `json:"vod_id"`
	VodName    string `json:"vod_name"`
	VodSub     string `json:"vod_sub"`
	VodPic     string `json:"vod_pic"`
	VodContent string `json:"vod_content"`
	VodYear    string `json:"vod_year"`
	VodArea    string `json:"vod_area"`
	VodClass   string `json:"vod_class"`
	VodPlayURL string `json:"vod_play_url"`
}

// apiResponse maps the top-level JSON structure.
type apiResponse struct {
	PageCount int        `json:"pagecount"`
	List      []apiMovie `json:"list"`
}

// Collector fetches all pages from apiURL and upserts movies into db.
type Collector struct {
	APIURL     string
	DB         *gorm.DB
	MaxWorkers int
	HTTPClient *http.Client
	// Hours, when > 0, limits collection to resources updated within the last
	// N hours by appending &h=N to every API request (incremental mode).
	Hours int
}

// New returns a Collector with sensible defaults (full collection).
func New(apiURL string, db *gorm.DB) *Collector {
	return &Collector{
		APIURL:     apiURL,
		DB:         db,
		MaxWorkers: defaultMaxWorkers,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// NewIncremental returns a Collector that only fetches resources updated
// within the last hours hours (incremental collection).
func NewIncremental(apiURL string, db *gorm.DB, hours int) *Collector {
	c := New(apiURL, db)
	c.Hours = hours
	return c
}

// Run fetches every page and upserts movies into the database.
func (c *Collector) Run() error {
	return c.RunWithContext(context.Background())
}

// RunWithContext fetches every page and upserts movies into the database,
// honouring the supplied context for cancellation and timeout.
func (c *Collector) RunWithContext(ctx context.Context) error {
	first, err := c.fetchPage(ctx, 1)
	if err != nil {
		return fmt.Errorf("collector: failed to fetch first page: %w", err)
	}

	pageCount := first.PageCount
	if pageCount < 1 {
		pageCount = 1
	}

	// Process page 1 results immediately.
	c.processMovies(first.List)

	if pageCount == 1 {
		return nil
	}

	// Worker pool for pages 2..pageCount.
	jobs := make(chan int, pageCount-1)
	for pg := 2; pg <= pageCount; pg++ {
		jobs <- pg
	}
	close(jobs)

	workers := c.MaxWorkers
	if workers < 1 {
		workers = defaultMaxWorkers
	}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pg := range jobs {
				resp, err := c.fetchPage(ctx, pg)
				if err != nil {
					log.Printf("collector: page %d fetch error: %v", pg, err)
					continue
				}
				c.processMovies(resp.List)
			}
		}()
	}
	wg.Wait()
	return nil
}

// fetchPage requests a single page from the API and decodes the response.
func (c *Collector) fetchPage(ctx context.Context, page int) (*apiResponse, error) {
	url := fmt.Sprintf("%s&pg=%d", c.APIURL, page)
	if c.Hours > 0 {
		url = fmt.Sprintf("%s&h=%d", url, c.Hours)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var ar apiResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		return nil, fmt.Errorf("json decode: %w", err)
	}
	return &ar, nil
}

// processMovies upserts each movie; parse errors are logged and skipped.
func (c *Collector) processMovies(list []apiMovie) {
	for _, am := range list {
		if err := c.upsertMovie(am); err != nil {
			log.Printf("collector: upsert error for vod_id=%d name=%q: %v", am.VodID, am.VodName, err)
		}
	}
}

// upsertMovie converts an apiMovie to DB records and performs an upsert.
func (c *Collector) upsertMovie(am apiMovie) error {
	if am.VodName == "" {
		return fmt.Errorf("empty vod_name")
	}

	thirdPartyID := fmt.Sprintf("%d", am.VodID)

	attrs := models.Movie{
		ThirdPartyID: thirdPartyID,
		Title:        am.VodName,
		SubTitle:     am.VodSub,
		Poster:       am.VodPic,
		Description:  am.VodContent,
		Year:         am.VodYear,
		Area:         am.VodArea,
		Class:        am.VodClass,
		UpdateTime:   time.Now(),
	}

	// Upsert: insert or update on conflict of ThirdPartyID.
	movie := models.Movie{ThirdPartyID: thirdPartyID}
	result := c.DB.
		Where(models.Movie{ThirdPartyID: thirdPartyID}).
		Assign(attrs).
		FirstOrCreate(&movie)
	if result.Error != nil {
		return result.Error
	}

	newSources := parseVideoSources(am.VodPlayURL, movie.ID)

	// Replace sources inside a transaction so reads and writes are consistent.
	return c.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("movie_id = ?", movie.ID).Delete(&models.VideoSource{}).Error; err != nil {
			return fmt.Errorf("delete old sources: %w", err)
		}
		if len(newSources) == 0 {
			// Orphan check: delete a movie that ends up with no video sources.
			return tx.Delete(&models.Movie{}, movie.ID).Error
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&newSources).Error
	})
}

// parseVideoSources parses the vod_play_url field.
// Format: "名称$URL#名称$URL#..."
func parseVideoSources(raw string, movieID uint) []models.VideoSource {
	if raw == "" {
		return nil
	}
	var sources []models.VideoSource
	for _, entry := range strings.Split(raw, "#") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "$", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
			log.Printf("collector: skipping malformed source entry %q", entry)
			continue
		}
		sources = append(sources, models.VideoSource{
			MovieID:   movieID,
			SourceKey: strings.TrimSpace(parts[0]),
			RawURL:    strings.TrimSpace(parts[1]),
		})
	}
	return sources
}
