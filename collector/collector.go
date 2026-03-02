package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"my-movie-site/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// htmlTagRe matches HTML tags for stripping purposes.
var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

const defaultMaxWorkers = 1

// flexInt handles vod_id values that may be returned as either a JSON number
// or a quoted string by different API sources.
type flexInt int

func (f *flexInt) UnmarshalJSON(data []byte) error {
	// Try integer first.
	var n int
	if err := json.Unmarshal(data, &n); err == nil {
		*f = flexInt(n)
		return nil
	}
	// Fall back to string.
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("flexInt: cannot parse %s: %w", data, err)
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("flexInt: cannot convert %q to int: %w", s, err)
	}
	*f = flexInt(n)
	return nil
}

// apiMovie maps the JSON fields returned by the remote API.
type apiMovie struct {
	VodID      flexInt `json:"vod_id"`
	VodName    string  `json:"vod_name"`
	VodSub     string  `json:"vod_sub"`
	VodPic     string  `json:"vod_pic"`
	VodContent string  `json:"vod_content"`
	VodYear    string  `json:"vod_year"`
	VodArea    string  `json:"vod_area"`
	VodClass   string  `json:"vod_class"`
	VodPlayURL string  `json:"vod_play_url"`
	VodTypeID  int     `json:"type_id"`
	TypeID1    int     `json:"type_id_1"`
	TypeName   string  `json:"type_name"`
	VodRemarks string  `json:"vod_remarks"`
}

// apiClass maps a single entry in the top-level class list returned by the API.
type apiClass struct {
	TypeID   int    `json:"type_id"`
	TypePID  int    `json:"type_pid"`
	TypeName string `json:"type_name"`
}

// stripHTML removes HTML tags from s and returns plain text.
func stripHTML(s string) string {
	return strings.TrimSpace(htmlTagRe.ReplaceAllString(s, ""))
}

// apiResponse maps the top-level JSON structure.
type apiResponse struct {
	PageCount int        `json:"pagecount"`
	List      []apiMovie `json:"list"`
	Class     []apiClass `json:"class"`
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
	// CollectionSourceID is the ID of the CollectionSource record this
	// collector is running for.  0 means no specific source (legacy mode).
	CollectionSourceID uint
	// SourceName is the display name of the collection source, stored on
	// each VideoSource record so the player can label the buttons.
	SourceName string
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

// RunAllFromDB loads every enabled CollectionSource from the database and runs
// a full collection pass for each one in sequence.
func RunAllFromDB(ctx context.Context, db *gorm.DB) error {
	var sources []models.CollectionSource
	if err := db.Where("enabled = ?", true).Find(&sources).Error; err != nil {
		return fmt.Errorf("collector: fetch sources from db: %w", err)
	}
	for _, src := range sources {
		c := New(src.APIURL, db)
		c.CollectionSourceID = src.ID
		c.SourceName = src.Name
		log.Printf("collector: running source %q (key=%s)", src.Name, src.SourceKey)
		if err := c.RunWithContext(ctx); err != nil {
			log.Printf("collector: source %q failed: %v", src.Name, err)
		}
	}
	return nil
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

	// Pre-populate category maps from the class list returned on the first page.
	if c.CollectionSourceID != 0 && len(first.Class) > 0 {
		c.populateCategoryMaps(first.Class)
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
			defer func() {
				if r := recover(); r != nil {
					log.Printf("collector: worker recovered from panic: %v", r)
				}
			}()
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
	u, err := url.Parse(c.APIURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("ac", "detail")
	q.Set("pg", strconv.Itoa(page))
	if c.Hours > 0 {
		q.Set("h", strconv.Itoa(c.Hours))
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
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

// processMovies upserts each movie; entries without a play URL are logged and
// skipped, and other errors are also logged and skipped.
func (c *Collector) processMovies(list []apiMovie) {
	for _, am := range list {
		if am.VodPlayURL == "" {
			log.Printf("collector: skipping vod_id=%d name=%q: vod_play_url is empty", int(am.VodID), am.VodName)
			continue
		}
		if err := c.upsertMovie(am); err != nil {
			log.Printf("collector: upsert error for vod_id=%d name=%q: %v", int(am.VodID), am.VodName, err)
		}
	}
}

// GetLocalCategoryID returns the local category ID for a given source and
// remote type ID.  If no mapping exists it inserts a placeholder record with
// LocalCategoryID = 0 (to-be-bound) and returns 0.  remoteName is stored on
// the placeholder row so admins can identify it in the back-end.
func (c *Collector) GetLocalCategoryID(sourceID uint, remoteTypeID string, remoteName string) uint {
	if sourceID == 0 || remoteTypeID == "" {
		return 0
	}
	var cm models.CategoryMap
	result := c.DB.
		Attrs(models.CategoryMap{RemoteName: remoteName}).
		FirstOrCreate(&cm, models.CategoryMap{SourceID: sourceID, RemoteTypeID: remoteTypeID})
	if result.Error != nil {
		log.Printf("collector: GetLocalCategoryID error: %v", result.Error)
		return 0
	}
	// Update RemoteName if the existing row has no name yet.
	if cm.RemoteName == "" && remoteName != "" {
		if err := c.DB.Model(&cm).Update("remote_name", remoteName).Error; err != nil {
			log.Printf("collector: GetLocalCategoryID update name error: %v", err)
		}
	}
	return cm.LocalCategoryID
}

// populateCategoryMaps inserts or updates CategoryMap rows for the remote class
// list returned on the first API page, filling in RemoteName for later manual
// binding.  Child categories (TypePID != 0) are stored with the display name
// "ParentName > ChildName" for easy identification in the admin back-end.
// It also auto-creates local Category records for both parent and child classes
// and wires CategoryMap.LocalCategoryID to the finest-grained local category.
func (c *Collector) populateCategoryMaps(classes []apiClass) {
	// Build a lookup map from TypeID → TypeName for parent resolution.
	nameByID := make(map[int]string, len(classes))
	for _, cl := range classes {
		nameByID[cl.TypeID] = cl.TypeName
	}

	// Pass 1: ensure a local Category exists for every top-level (parent) class
	// and update the corresponding CategoryMap entry.
	localParentCatByRemoteID := make(map[int]uint, len(classes))
	for _, cl := range classes {
		if cl.TypePID != 0 {
			continue
		}
		typeIDStr := fmt.Sprintf("%d", cl.TypeID)

		// Find or create the local top-level Category.
		var cat models.Category
		err := c.DB.Where("name = ? AND parent_id = 0", cl.TypeName).First(&cat).Error
		if err == gorm.ErrRecordNotFound {
			cat = models.Category{Name: cl.TypeName, ParentID: 0, Enabled: true}
			if createErr := c.DB.Create(&cat).Error; createErr != nil {
				log.Printf("collector: populateCategoryMaps create parent category %q: %v", cl.TypeName, createErr)
			}
		} else if err != nil {
			log.Printf("collector: populateCategoryMaps find parent category %q: %v", cl.TypeName, err)
		}
		if cat.ID != 0 {
			localParentCatByRemoteID[cl.TypeID] = cat.ID
		}

		// Upsert the CategoryMap row for this parent class.
		c.upsertCategoryMap(typeIDStr, cl.TypeName, cat.ID)
	}

	// Pass 2: ensure a local Category exists for every child class and update
	// the corresponding CategoryMap entry to point to the child Category.
	for _, cl := range classes {
		if cl.TypePID == 0 {
			continue
		}
		typeIDStr := fmt.Sprintf("%d", cl.TypeID)
		parentName := nameByID[cl.TypePID]
		displayName := cl.TypeName
		if parentName != "" {
			displayName = parentName + " > " + cl.TypeName
		}

		parentLocalID := localParentCatByRemoteID[cl.TypePID]

		// Find or create the local child Category.
		var cat models.Category
		if parentLocalID != 0 {
			err := c.DB.Where("name = ? AND parent_id = ?", cl.TypeName, parentLocalID).First(&cat).Error
			if err == gorm.ErrRecordNotFound {
				cat = models.Category{Name: cl.TypeName, ParentID: parentLocalID, Enabled: true}
				if createErr := c.DB.Create(&cat).Error; createErr != nil {
					log.Printf("collector: populateCategoryMaps create child category %q: %v", cl.TypeName, createErr)
				}
			} else if err != nil {
				log.Printf("collector: populateCategoryMaps find child category %q: %v", cl.TypeName, err)
			}
		}

		// Upsert the CategoryMap row for this child class, pointing to the child
		// local Category (finest granularity).
		c.upsertCategoryMap(typeIDStr, displayName, cat.ID)
	}
}

// upsertCategoryMap inserts or selectively updates a CategoryMap row.
// If the row does not exist it is created with the given remoteName and
// localCategoryID.  If it already exists, only blank remoteName / zero
// localCategoryID fields are backfilled.
func (c *Collector) upsertCategoryMap(remoteTypeID, remoteName string, localCategoryID uint) {
	var cm models.CategoryMap
	err := c.DB.Where("source_id = ? AND remote_type_id = ?", c.CollectionSourceID, remoteTypeID).First(&cm).Error
	if err == gorm.ErrRecordNotFound {
		cm = models.CategoryMap{
			SourceID:        c.CollectionSourceID,
			RemoteTypeID:    remoteTypeID,
			RemoteName:      remoteName,
			LocalCategoryID: localCategoryID,
		}
		if createErr := c.DB.Create(&cm).Error; createErr != nil {
			log.Printf("collector: upsertCategoryMap create error: %v", createErr)
		}
		return
	}
	if err != nil {
		log.Printf("collector: upsertCategoryMap lookup error: %v", err)
		return
	}
	updates := map[string]interface{}{}
	if cm.RemoteName == "" && remoteName != "" {
		updates["remote_name"] = remoteName
	}
	if cm.LocalCategoryID == 0 && localCategoryID != 0 {
		updates["local_category_id"] = localCategoryID
	}
	if len(updates) > 0 {
		if updateErr := c.DB.Model(&cm).Updates(updates).Error; updateErr != nil {
			log.Printf("collector: upsertCategoryMap update error: %v", updateErr)
		}
	}
}

// upsertMovie converts an apiMovie to DB records and performs an upsert.
// Movies are matched by Title to enable cross-source aggregation.
func (c *Collector) upsertMovie(am apiMovie) error {
	if am.VodName == "" {
		return fmt.Errorf("empty vod_name")
	}

	localCatID := c.GetLocalCategoryID(c.CollectionSourceID, fmt.Sprintf("%d", am.VodTypeID), am.TypeName)

	description := stripHTML(am.VodContent)

	attrs := map[string]interface{}{
		"sub_title":   am.VodSub,
		"poster":      am.VodPic,
		"description": description,
		"year":        am.VodYear,
		"area":        am.VodArea,
		"class":       am.VodClass,
		"category_id": localCatID,
		"update_time": time.Now(),
	}

	// Look up by Title for multi-source aggregation.
	var movie models.Movie
	result := c.DB.Where("title = ?", am.VodName).First(&movie)
	if result.Error == gorm.ErrRecordNotFound {
		movie = models.Movie{
			Title:        am.VodName,
			ThirdPartyID: fmt.Sprintf("%d", int(am.VodID)),
			SubTitle:     am.VodSub,
			Poster:       am.VodPic,
			Description:  description,
			Year:         am.VodYear,
			Area:         am.VodArea,
			Class:        am.VodClass,
			CategoryID:   localCatID,
			UpdateTime:   time.Now(),
		}
		if err := c.DB.Create(&movie).Error; err != nil {
			return err
		}
	} else if result.Error != nil {
		return result.Error
	} else {
		// Update movie info; most recent collection wins.
		if err := c.DB.Model(&movie).Updates(attrs).Error; err != nil {
			return err
		}
	}

	newSources := parseVideoSources(am.VodPlayURL, movie.ID, c.CollectionSourceID, c.SourceName)

	// Replace sources for this collection source inside a transaction.
	return c.DB.Transaction(func(tx *gorm.DB) error {
		del := tx.Where("movie_id = ?", movie.ID)
		if c.CollectionSourceID != 0 {
			// Only delete sources that belong to the current collection source.
			del = del.Where("collection_source_id = ?", c.CollectionSourceID)
		}
		if err := del.Delete(&models.VideoSource{}).Error; err != nil {
			return fmt.Errorf("delete old sources: %w", err)
		}

		if len(newSources) == 0 {
			// Orphan check: delete movie only when it has no sources at all.
			var remaining int64
			tx.Model(&models.VideoSource{}).Where("movie_id = ?", movie.ID).Count(&remaining)
			if remaining == 0 {
				return tx.Delete(&models.Movie{}, movie.ID).Error
			}
			return nil
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&newSources).Error
	})
}

// parseVideoSources parses the vod_play_url field.
// Format: "名称$URL#名称$URL#..."
func parseVideoSources(raw string, movieID uint, collectionSourceID uint, sourceName string) []models.VideoSource {
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
			MovieID:            movieID,
			CollectionSourceID: collectionSourceID,
			SourceKey:          strings.TrimSpace(parts[0]),
			SourceName:         sourceName,
			RawURL:             strings.TrimSpace(parts[1]),
		})
	}
	return sources
}
