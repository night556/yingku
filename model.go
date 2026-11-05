package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Config struct {
	MediaRoot      string        `json:"mediaRoot"`
	Concurrency    int           `json:"concurrency"`
	TMDBSecret     string        `json:"tmdbSecret,omitempty"`
	BaiduAppSecret string        `json:"baiduAppSecret,omitempty"`
	ExportNFO      bool          `json:"exportNfo"`
	AutoMetadata   bool          `json:"autoMetadata"`
	PublicURL      string        `json:"publicURL,omitempty"`
	CastBaseURL    string        `json:"castBaseURL,omitempty"`
	AI             AIConfig      `json:"ai"`
	Network        NetworkConfig `json:"network"`
}
type AIConfig struct {
	Enabled  bool   `json:"enabled"`
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
	Secret   string `json:"secret,omitempty"`
}
type Source struct {
	Pending  bool   `json:"-"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Root     string `json:"root"`
	URL      string `json:"url,omitempty"`
	Username string `json:"username,omitempty"`
	Secret   string `json:"secret,omitempty"`
	ClientID string `json:"clientId,omitempty"`
	Created  string `json:"created"`
}
type Credentials struct {
	Cookie       string `json:"cookie,omitempty"`
	Password     string `json:"password,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	AccessToken  string `json:"accessToken,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
	Expires      int64  `json:"expires,omitempty"`
}
type Entry struct {
	Key      string `json:"key"`
	RemoteID string `json:"remoteId,omitempty"`
	Name     string `json:"name"`
	Dir      bool   `json:"dir"`
	Size     int64  `json:"size"`
	Modified string `json:"modified,omitempty"`
	ETag     string `json:"etag,omitempty"`
	Media    bool   `json:"media"`
}
type Episode struct {
	Season       int     `json:"season"`
	Number       int     `json:"number"`
	Name         string  `json:"name"`
	Overview     string  `json:"overview"`
	Still        string  `json:"still"`
	AirDate      string  `json:"airDate"`
	Runtime      int     `json:"runtime,omitempty"`
	Rating       float64 `json:"rating,omitempty"`
	RatingSource string  `json:"ratingSource,omitempty"`
}
type Item struct {
	ID               string          `json:"id"`
	SeparateSeries   bool            `json:"separateSeries,omitempty"`
	Type             string          `json:"type"`
	Title            string          `json:"title"`
	OriginalTitle    string          `json:"originalTitle,omitempty"`
	Year             string          `json:"year"`
	TMDBID           int             `json:"tmdbId"`
	MetadataProvider string          `json:"metadataProvider,omitempty"`
	MetadataID       string          `json:"metadataId,omitempty"`
	MetadataURL      string          `json:"metadataUrl,omitempty"`
	MetadataNote     string          `json:"metadataNote,omitempty"`
	Overview         string          `json:"overview"`
	Poster           string          `json:"poster"`
	Backdrop         string          `json:"backdrop"`
	Rating           float64         `json:"rating"`
	Genres           []string        `json:"genres"`
	Cast             []string        `json:"cast"`
	Episodes         []Episode       `json:"episodes"`
	MetadataStatus   string          `json:"metadataStatus"`
	MetadataError    string          `json:"metadataError,omitempty"`
	Created          string          `json:"created"`
	Details          MetadataDetails `json:"details"`
	LocalFields      []string        `json:"localFields,omitempty"`
}
type Subtitle struct {
	Name string `json:"name"`
	Path string `json:"-"`
}
type MediaFile struct {
	ID             string            `json:"id"`
	ItemID         string            `json:"itemId"`
	SourceID       string            `json:"sourceId"`
	SourceKey      string            `json:"sourceKey"`
	SourceRemoteID string            `json:"sourceRemoteId,omitempty"`
	Name           string            `json:"name"`
	Season         int               `json:"season"`
	Episode        int               `json:"episode"`
	EpisodeEnd     int               `json:"episodeEnd,omitempty"`
	Destination    string            `json:"destination"`
	Reference      bool              `json:"reference"`
	Status         string            `json:"status"`
	Size           int64             `json:"size"`
	Position       float64           `json:"position"`
	ProgressToken  string            `json:"progressToken,omitempty"`
	LastPlayed     string            `json:"lastPlayed,omitempty"`
	Availability   *FileAvailability `json:"availability,omitempty"`
	Duration       float64           `json:"duration"`
	VideoWidth     int               `json:"videoWidth,omitempty"`
	VideoHeight    int               `json:"videoHeight,omitempty"`
	Subtitles      []Subtitle        `json:"subtitles"`
	Created        string            `json:"created"`
}

// Subtitle paths must survive restarts but are never sent by the public state API.
type SavedSubtitle struct {
	Name string `json:"name"`
	Path string `json:"path"`
}
type Task struct {
	ID             string  `json:"id"`
	FileID         string  `json:"fileId"`
	Status         string  `json:"status"`
	Bytes          int64   `json:"bytes"`
	Total          int64   `json:"total"`
	Speed          float64 `json:"speed"`
	Error          string  `json:"error,omitempty"`
	ETag           string  `json:"etag,omitempty"`
	LastModified   string  `json:"lastModified,omitempty"`
	SourceModified string  `json:"sourceModified,omitempty"`
	Created        string  `json:"created"`
}
type State struct {
	Version       int                        `json:"version"`
	Config        Config                     `json:"config"`
	Sources       []Source                   `json:"sources"`
	Items         []Item                     `json:"items"`
	Files         []MediaFile                `json:"files"`
	Tasks         []Task                     `json:"tasks"`
	SubtitlePaths map[string][]SavedSubtitle `json:"subtitlePaths"`
	ItemAliases   map[string]string          `json:"itemAliases,omitempty"`
	FileDeletes   map[string]bool            `json:"fileDeletes,omitempty"`
}
type App struct {
	mu                 sync.Mutex
	baiduMu            sync.Mutex
	quarkMu            sync.Mutex
	cloudAuth          map[string]*CloudAuthSession
	metadataJobs       map[string]string
	s                  State
	dir                string
	managedMediaDir    string
	publicURL          string
	aead               cipher.AEAD
	csrf               string
	client             *http.Client
	active             map[string]func()
	closing            bool
	scans              map[string]*ScanSession
	aiClient           *http.Client
	dlnaMu             sync.Mutex
	listenAddress      string
	sessions           map[string]webSession
	loginAttempts      map[string]loginAttempt
	devices            map[string]DLNADevice
	casts              map[string]*CastSession
	discoverDevices    DLNADiscoverer
	deletePreviews     map[string]diskDeletePreview
	fileDeleteWarnings []string
	fileHealth         map[string]fileHealthEntry
	fileHealthBusy     bool
}

func uid() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func now() string { return time.Now().UTC().Format(time.RFC3339) }

type appOptions struct {
	MediaDir  string
	PublicURL string
}

func newApp(dir string) (*App, error) {
	return newAppWithOptions(dir, appOptions{})
}

func newAppWithOptions(dir string, options appOptions) (*App, error) {
	publicURL, err := normalizePublicURL(options.PublicURL)
	if err != nil {
		return nil, err
	}
	mediaDir := strings.TrimSpace(options.MediaDir)
	if mediaDir != "" {
		if !filepath.IsAbs(mediaDir) {
			return nil, errors.New("启动配置中的媒体目录需要是绝对路径")
		}
		mediaDir = filepath.Clean(mediaDir)
		if err := os.MkdirAll(mediaDir, 0755); err != nil {
			return nil, fmt.Errorf("无法创建媒体目录：%w", err)
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(abs, "secret.key")
	key, err := os.ReadFile(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err == nil {
			err = os.WriteFile(keyPath, key, 0600)
		}
	}
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	a := &App{dir: abs, managedMediaDir: mediaDir, publicURL: publicURL, aead: aead, csrf: uid(), client: &http.Client{Timeout: 45 * time.Second}, active: map[string]func(){}, metadataJobs: map[string]string{}}
	b, err := os.ReadFile(filepath.Join(abs, "state.json"))
	if err == nil {
		if err = json.Unmarshal(b, &a.s); err != nil {
			return nil, fmt.Errorf("读取资料库失败（原文件已保留）: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	} else {
		a.s = State{Version: 2, Config: Config{MediaRoot: filepath.Join(abs, "media"), Concurrency: 2, ExportNFO: true, AutoMetadata: true}, Sources: []Source{}, Items: []Item{}, Files: []MediaFile{}, Tasks: []Task{}}
	}
	if err = a.recoverDiskDeletes(); err != nil {
		return nil, err
	}
	if len(b) > 0 && a.s.Version < 4 {
		backup := filepath.Join(abs, "state-before-details-v4.json")
		if _, statErr := os.Stat(backup); errors.Is(statErr, os.ErrNotExist) {
			if err = atomicWrite(backup, b, 0600); err != nil {
				return nil, err
			}
		} else if statErr != nil {
			return nil, statErr
		}
	}
	if len(b) > 0 && a.s.Version < 5 {
		backup := filepath.Join(abs, "state-before-ai-v5.json")
		if _, statErr := os.Stat(backup); errors.Is(statErr, os.ErrNotExist) {
			if err = atomicWrite(backup, b, 0600); err != nil {
				return nil, err
			}
		} else if statErr != nil {
			return nil, statErr
		}
	}
	if len(b) > 0 && a.s.Version < 6 {
		backup := filepath.Join(abs, "state-before-network-v6.json")
		if _, statErr := os.Stat(backup); errors.Is(statErr, os.ErrNotExist) {
			if err = atomicWrite(backup, b, 0600); err != nil {
				return nil, err
			}
		} else if statErr != nil {
			return nil, statErr
		}
	}
	if len(b) > 0 && a.s.Version < 7 {
		backup := filepath.Join(abs, "state-before-listener-v7.json")
		if _, statErr := os.Stat(backup); errors.Is(statErr, os.ErrNotExist) {
			if err = atomicWrite(backup, b, 0600); err != nil {
				return nil, err
			}
		} else if statErr != nil {
			return nil, statErr
		}
	}
	if a.s.Config.Network.Hash == "" {
		a.s.Config.Network.NoPassword = true
	}
	if a.s.Version < 2 {
		a.s.Config.AutoMetadata = true
		a.s.Version = 2
	}
	if a.s.SubtitlePaths == nil {
		a.s.SubtitlePaths = map[string][]SavedSubtitle{}
	}
	if a.s.Config.Concurrency < 1 {
		a.s.Config.Concurrency = 2
	}
	if mediaDir != "" {
		// Only the default for future imports changes; existing file and source paths stay intact.
		a.s.Config.MediaRoot = mediaDir
	}
	for i := range a.s.Files {
		a.s.Files[i].Subtitles = []Subtitle{}
		for _, sub := range a.s.SubtitlePaths[a.s.Files[i].ID] {
			a.s.Files[i].Subtitles = append(a.s.Files[i].Subtitles, Subtitle{Name: sub.Name, Path: sub.Path})
		}
	}
	for i := range a.s.Tasks {
		if a.s.Tasks[i].Status == "finalizing" {
			for j := range a.s.Files {
				if a.s.Files[j].ID != a.s.Tasks[i].FileID {
					continue
				}
				f := &a.s.Files[j]
				part := f.Destination + ".yingku-" + a.s.Tasks[i].ID + ".part"
				finalInfo, finalErr := os.Stat(f.Destination)
				partInfo, partErr := os.Stat(part)
				if finalErr == nil && finalInfo.Size() == a.s.Tasks[i].Total && (errors.Is(partErr, os.ErrNotExist) || partErr == nil && os.SameFile(finalInfo, partInfo)) {
					a.s.Tasks[i].Status = "completed"
					f.Status = "completed"
					if partErr == nil {
						_ = os.Remove(part)
					}
				} else {
					a.s.Tasks[i].Status = "paused"
					f.Status = "paused"
					a.s.Tasks[i].Error = "入库时服务中断，请检查目标文件后继续"
				}
			}
		}
		if a.s.Tasks[i].Status == "downloading" {
			a.s.Tasks[i].Status = "paused"
			a.s.Tasks[i].Speed = 0
			a.s.Tasks[i].Error = "上次运行中断，点击继续恢复下载"
			for j := range a.s.Files {
				if a.s.Files[j].ID == a.s.Tasks[i].FileID {
					a.s.Files[j].Status = "paused"
				}
			}
		}
	}
	for i := range a.s.Items {
		if a.s.Items[i].MetadataStatus == "loading" {
			a.s.Items[i].MetadataStatus = "failed"
			a.s.Items[i].MetadataError = "上次资料获取中断，可以重新刮削"
		}
	}
	if err = a.repairRemovedLocalSourcesLocked(b); err != nil {
		return nil, err
	}
	if err = a.regroupSeriesLocked(); err != nil {
		return nil, err
	}
	a.s.Version = 7
	if err = a.saveLocked(); err != nil {
		return nil, err
	}
	return a, nil
}
func (a *App) encrypt(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	n := make([]byte, a.aead.NonceSize())
	if _, err = rand.Read(n); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(a.aead.Seal(n, n, b, nil)), nil
}
func (a *App) decrypt(v string, out any) error {
	if v == "" {
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(b) < a.aead.NonceSize() {
		return errors.New("凭据文件损坏")
	}
	n := a.aead.NonceSize()
	b, err = a.aead.Open(nil, b[:n], b[n:], nil)
	if err != nil {
		return errors.New("无法解密凭据，请检查 secret.key 是否与资料库配套")
	}
	return json.Unmarshal(b, out)
}
func atomicWrite(name string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(name), ".yingku-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, name)
}
func (a *App) saveLocked() error {
	b, err := json.MarshalIndent(a.s, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(a.dir, "state.json"), b, 0600)
}
func (a *App) source(id string) (Source, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.s.Sources {
		if s.ID == id {
			return s, nil
		}
	}
	return Source{}, errors.New("来源不存在")
}
func (a *App) file(id string) (MediaFile, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, f := range a.s.Files {
		if f.ID == id {
			return f, nil
		}
	}
	return MediaFile{}, errors.New("文件不存在")
}
func (a *App) item(id string) (Item, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id = a.canonicalItemIDLocked(id)
	for _, it := range a.s.Items {
		if it.ID == id {
			it.Genres = append([]string{}, it.Genres...)
			it.Cast = append([]string{}, it.Cast...)
			it.Episodes = append([]Episode{}, it.Episodes...)
			it.LocalFields = append([]string{}, it.LocalFields...)
			it.Details = cloneDetails(it.Details)
			return it, nil
		}
	}
	return Item{}, errors.New("作品不存在")
}
func (a *App) publicState() any {
	a.mu.Lock()
	defer a.mu.Unlock()
	sources := make([]map[string]any, 0, len(a.s.Sources))
	for _, s := range a.s.Sources {
		sources = append(sources, map[string]any{"id": s.ID, "name": s.Name, "type": s.Type, "root": s.Root, "url": s.URL, "username": s.Username, "clientId": s.ClientID, "created": s.Created})
	}
	items := append([]Item{}, a.s.Items...)
	for i := range items {
		items[i].Genres = append([]string{}, items[i].Genres...)
		items[i].Cast = append([]string{}, items[i].Cast...)
		items[i].Episodes = append([]Episode{}, items[i].Episodes...)
		items[i].Details = cloneDetails(items[i].Details)
		items[i].LocalFields = append([]string{}, items[i].LocalFields...)
	}
	files := append([]MediaFile{}, a.s.Files...)
	for i := range files {
		files[i].Subtitles = append([]Subtitle{}, files[i].Subtitles...)
		files[i].EpisodeEnd = fileEpisodeEnd(files[i])
		if files[i].Status == "completed" {
			value := a.fileAvailabilityLocked(files[i])
			files[i].Availability = &value
		}
	}
	aliases := map[string]string{}
	for id, target := range a.s.ItemAliases {
		aliases[id] = target
	}
	return map[string]any{"csrf": a.csrf, "version": appVersion, "storage": map[string]any{"fileDeleteWarnings": append([]string{}, a.fileDeleteWarnings...), "dataDir": a.dir, "mediaDirManaged": a.managedMediaDir != "", "publicURL": a.effectivePublicURLLocked()}, "config": map[string]any{"mediaRoot": a.s.Config.MediaRoot, "concurrency": a.s.Config.Concurrency, "exportNfo": a.s.Config.ExportNFO, "autoMetadata": a.s.Config.AutoMetadata, "tmdbConfigured": a.s.Config.TMDBSecret != "", "ai": map[string]any{"enabled": a.s.Config.AI.Enabled, "endpoint": a.s.Config.AI.Endpoint, "model": a.s.Config.AI.Model, "keyConfigured": a.s.Config.AI.Secret != ""}}, "sources": sources, "items": items, "files": files, "tasks": append([]Task{}, a.s.Tasks...), "itemAliases": aliases}
}
func safeName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, s)
	s = strings.Trim(s, " .")
	r := []rune(s)
	if len(r) > 100 {
		s = string(r[:100])
	}
	base := strings.ToUpper(strings.SplitN(s, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
		s = "_" + s
	}
	if s == "" {
		s = "未命名"
	}
	return s
}
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func mediaExt(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp4", ".m4v", ".mkv", ".webm", ".mov", ".avi", ".ts":
		return true
	}
	return false
}
