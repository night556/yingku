package yingku

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func cleanKey(key string) (string, error) {
	if strings.ContainsAny(key, "\x00\\") {
		return "", errors.New("无效的来源路径")
	}
	for _, s := range strings.Split(key, "/") {
		if s == ".." {
			return "", errors.New("路径不能越出来源目录")
		}
	}
	return path.Clean("/" + strings.TrimPrefix(key, "/")), nil
}
func localPath(s Source, key string) (string, error) {
	k, err := cleanKey(key)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(s.Root)
	if err != nil {
		return "", err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	p, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(k, "/"))))
	if err != nil {
		return "", err
	}
	if !within(root, p) {
		return "", errors.New("文件位于来源目录之外")
	}
	return p, nil
}
func davURL(s Source, key string) (*url.URL, error) {
	u, err := url.Parse(s.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, errors.New("WebDAV 地址需要是 HTTP(S) 地址，账号请单独填写")
	}
	k, err := cleanKey(key)
	if err != nil {
		return nil, err
	}
	u.Path = strings.TrimRight(u.Path, "/") + k
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u, nil
}
func (a *App) credentials(s Source) (Credentials, error) {
	var c Credentials
	err := a.decrypt(s.Secret, &c)
	return c, err
}
func (a *App) list(ctx context.Context, s Source, key string) ([]Entry, error) {
	k, err := cleanKey(key)
	if err != nil {
		return nil, err
	}
	entries := []Entry{}
	switch s.Type {
	case "local":
		p, err := localPath(s, k)
		if err != nil {
			return nil, fmt.Errorf("目录无法读取: %w", err)
		}
		ds, err := os.ReadDir(p)
		if err != nil {
			return nil, err
		}
		if len(ds) > 10000 {
			return nil, errors.New("目录文件超过 10000 个，请选择更小的子目录")
		}
		for _, d := range ds {
			if strings.HasPrefix(d.Name(), ".") {
				continue
			}
			if d.Type()&os.ModeSymlink != 0 {
				continue
			}
			info, err := d.Info()
			if err != nil {
				continue
			}
			if !info.IsDir() && !info.Mode().IsRegular() {
				continue
			}
			entries = append(entries, Entry{Key: path.Join(k, d.Name()), Name: d.Name(), Dir: info.IsDir(), Size: info.Size(), Modified: info.ModTime().UTC().Format(time.RFC3339Nano), Media: mediaExt(d.Name())})
		}
	case "webdav":
		u, err := davURL(s, k)
		if err != nil {
			return nil, err
		}
		if !strings.HasSuffix(u.Path, "/") {
			u.Path += "/"
		}
		c, err := a.credentials(s)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, "PROPFIND", u.String(), strings.NewReader(`<?xml version="1.0" encoding="utf-8"?><d:propfind xmlns:d="DAV:"><d:prop><d:displayname/><d:resourcetype/><d:getcontentlength/><d:getlastmodified/><d:getetag/></d:prop></d:propfind>`))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Depth", "1")
		req.Header.Set("Content-Type", "application/xml; charset=utf-8")
		if s.Username != "" {
			req.SetBasicAuth(s.Username, c.Password)
		}
		// Never forward WebDAV passwords to a redirect target.
		client := *a.client
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
		resp, err := client.Do(req)
		if err != nil {
			return nil, errors.New("WebDAV 无法连接，请检查服务地址和网络")
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 && resp.StatusCode != 200 {
			return nil, fmt.Errorf("WebDAV 目录读取失败（HTTP %d），请检查路径和账号", resp.StatusCode)
		}
		var doc struct {
			Responses []struct {
				Href      string `xml:"href"`
				Propstats []struct {
					Status string `xml:"status"`
					Prop   struct {
						Name     string `xml:"displayname"`
						Size     int64  `xml:"getcontentlength"`
						Modified string `xml:"getlastmodified"`
						ETag     string `xml:"getetag"`
						Resource struct {
							Collection *struct{} `xml:"collection"`
						} `xml:"resourcetype"`
					} `xml:"prop"`
				} `xml:"propstat"`
			} `xml:"response"`
		}
		if err = xml.NewDecoder(io.LimitReader(resp.Body, 12<<20)).Decode(&doc); err != nil {
			return nil, errors.New("WebDAV 返回的目录格式无效")
		}
		base, _ := url.Parse(s.URL)
		prefix := strings.TrimRight(base.Path, "/")
		for _, r := range doc.Responses {
			href, er := url.Parse(r.Href)
			if er != nil {
				continue
			}
			if href.IsAbs() && (href.Host != base.Host || href.Scheme != base.Scheme) {
				continue
			}
			resolved := u.ResolveReference(href)
			p := strings.TrimRight(resolved.Path, "/")
			if p == strings.TrimRight(u.Path, "/") {
				continue
			}
			if prefix != "" && !strings.HasPrefix(p, prefix+"/") {
				continue
			}
			rel, er := cleanKey(strings.TrimPrefix(p, prefix))
			if er != nil || path.Dir(rel) != k {
				continue
			}
			for _, ps := range r.Propstats {
				if ps.Status != "" && !strings.Contains(ps.Status, " 200 ") {
					continue
				}
				name := path.Base(rel)
				entries = append(entries, Entry{Key: rel, Name: name, Dir: ps.Prop.Resource.Collection != nil, Size: ps.Prop.Size, Modified: ps.Prop.Modified, ETag: ps.Prop.ETag, Media: mediaExt(name)})
				break
			}
		}
	case "quark":
		entries, err = a.listQuark(ctx, &s, k)
		if err != nil {
			return nil, err
		}
	case "baidu":
		root := path.Clean("/" + strings.TrimPrefix(s.Root, "/"))
		dir := path.Join(root, strings.TrimPrefix(k, "/"))
		for start := 0; start < 10000; start += 1000 {
			var result struct {
				List []struct {
					ID       json.Number `json:"fs_id"`
					Name     string      `json:"server_filename"`
					Path     string      `json:"path"`
					Size     int64       `json:"size"`
					Dir      int         `json:"isdir"`
					Modified int64       `json:"server_mtime"`
				} `json:"list"`
			}
			if err := a.baiduAPI(ctx, s, "/xpan/file", url.Values{"method": {"list"}, "dir": {dir}, "start": {strconv.Itoa(start)}, "limit": {"1000"}, "order": {"name"}}, &result); err != nil {
				return nil, err
			}
			for _, f := range result.List {
				if root != "/" && !strings.HasPrefix(f.Path, root+"/") {
					continue
				}
				rel := f.Path
				if root != "/" {
					rel = strings.TrimPrefix(rel, root)
				}
				entries = append(entries, Entry{Key: rel, RemoteID: f.ID.String(), Name: f.Name, Dir: f.Dir == 1, Size: f.Size, Modified: time.Unix(f.Modified, 0).UTC().Format(time.RFC3339), Media: mediaExt(f.Name)})
			}
			if len(result.List) < 1000 {
				break
			}
			if start == 9000 {
				return nil, errors.New("目录文件过多，请选择更小的子目录")
			}
		}
	default:
		return nil, errors.New("不支持的来源类型")
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}
		return naturalLess(entries[i].Name, entries[j].Name)
	})
	return entries, nil
}
func (a *App) inspectEntry(ctx context.Context, s Source, e Entry) (Entry, error) {
	k, err := cleanKey(e.Key)
	if err != nil {
		return Entry{}, err
	}
	entries, err := a.list(ctx, s, path.Dir(k))
	if err != nil {
		return Entry{}, err
	}
	for _, f := range entries {
		if f.Key == k && !f.Dir && f.Media {
			return f, nil
		}
	}
	return Entry{}, errors.New("找不到所选视频文件，请刷新来源目录")
}
func (a *App) refreshBaidu(ctx context.Context, s Source) (Credentials, error) {
	c, err := a.credentials(s)
	if err != nil {
		return c, err
	}
	if c.RefreshToken == "" || s.ClientID == "" || c.ClientSecret == "" {
		return c, errors.New("百度授权已失效，请在来源设置中重新授权")
	}
	q := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {c.RefreshToken}, "client_id": {s.ClientID}, "client_secret": {c.ClientSecret}}
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://openapi.baidu.com/oauth/2.0/token", strings.NewReader(q.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var result struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int64  `json:"expires_in"`
		Error   string `json:"error"`
	}
	if err = a.requestPrivateJSON(req, &result); err != nil {
		return c, err
	}
	if result.Error != "" || result.Access == "" {
		return c, errors.New("百度授权刷新失败，请检查应用权限并重新授权")
	}
	c.AccessToken = result.Access
	if result.Refresh != "" {
		c.RefreshToken = result.Refresh
	}
	c.Expires = time.Now().Unix() + result.Expires
	secret, err := a.encrypt(c)
	if err != nil {
		return c, err
	}
	if s.Pending {
		return c, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.s.Sources {
		if a.s.Sources[i].ID == s.ID {
			a.s.Sources[i].Secret = secret
			err = a.saveLocked()
			break
		}
	}
	return c, err
}
func (a *App) baiduAPI(ctx context.Context, s Source, endpoint string, q url.Values, out any) error {
	a.baiduMu.Lock()
	defer a.baiduMu.Unlock()
	// Retrieve the latest persisted token when another request has refreshed it.
	if latest, err := a.source(s.ID); err == nil && !s.Pending {
		s = latest
	}
	c, err := a.credentials(s)
	if err != nil {
		return err
	}
	if c.AccessToken == "" || (c.Expires > 0 && c.Expires < time.Now().Unix()+60) {
		c, err = a.refreshBaidu(ctx, s)
		if err != nil {
			return err
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		q.Set("access_token", c.AccessToken)
		u := "https://pan.baidu.com/rest/2.0" + endpoint + "?" + q.Encode()
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("User-Agent", "pan.baidu.com")
		var raw json.RawMessage
		if err = a.requestPrivateJSON(req, &raw); err != nil {
			return err
		}
		var status struct {
			Errno     int `json:"errno"`
			ErrorCode int `json:"error_code"`
		}
		if err = json.Unmarshal(raw, &status); err != nil {
			return err
		}
		code := status.Errno
		if code == 0 {
			code = status.ErrorCode
		}
		if code == 0 {
			return json.Unmarshal(raw, out)
		}
		if attempt == 0 && (code == 110 || code == 111 || code == -6 || code == 20016 || code == 20017) {
			c, err = a.refreshBaidu(ctx, s)
			if err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("百度接口返回错误 %d，请检查授权、目录权限与账号状态", code)
	}
	return errors.New("百度接口请求失败")
}
func (a *App) requestJSON(req *http.Request, out any) error {
	resp, err := a.client.Do(req)
	if err != nil {
		return errors.New("远程服务无法连接，请检查网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("远程服务返回 HTTP %d", resp.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out); err != nil {
		return errors.New("远程服务返回了无效的数据")
	}
	return nil
}

func (a *App) requestPrivateJSON(req *http.Request, out any) error {
	client := *a.client
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("授权服务无法连接，请检查网络后重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("授权服务返回 HTTP %d，请重新授权或稍后重试", resp.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out); err != nil {
		return errors.New("授权服务返回了无效的数据")
	}
	return nil
}

type ReaderInfo struct {
	Reader       io.ReadCloser
	Total        int64
	Offset       int64
	ETag         string
	LastModified string
}

func (a *App) openSource(ctx context.Context, s Source, f MediaFile, t Task) (ReaderInfo, error) {
	offset := t.Bytes
	if s.Type == "local" {
		p, err := localPath(s, f.SourceKey)
		if err != nil {
			return ReaderInfo{}, err
		}
		file, err := os.Open(p)
		if err != nil {
			return ReaderInfo{}, err
		}
		st, err := file.Stat()
		if err != nil {
			file.Close()
			return ReaderInfo{}, err
		}
		if !st.Mode().IsRegular() {
			file.Close()
			return ReaderInfo{}, errors.New("来源不是普通文件")
		}
		if st.Size() != f.Size || (t.SourceModified != "" && st.ModTime().UTC().Format(time.RFC3339Nano) != t.SourceModified) {
			file.Close()
			return ReaderInfo{}, errors.New("来源文件已变更，请重新导入")
		}
		if offset > st.Size() {
			offset = 0
		}
		if _, err = file.Seek(offset, 0); err != nil {
			file.Close()
			return ReaderInfo{}, err
		}
		return ReaderInfo{Reader: file, Total: st.Size(), Offset: offset}, nil
	}
	var remote string
	var err error
	headers := http.Header{}
	switch s.Type {
	case "quark":
		remote, headers, err = a.quarkDownload(ctx, s, f, t)
		if err != nil {
			return ReaderInfo{}, err
		}
	case "webdav":
		u, err := davURL(s, f.SourceKey)
		if err != nil {
			return ReaderInfo{}, err
		}
		remote = u.String()
		c, err := a.credentials(s)
		if err != nil {
			return ReaderInfo{}, err
		}
		if s.Username != "" {
			headers.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(s.Username+":"+c.Password)))
		}
	case "baidu":
		if f.SourceRemoteID == "" {
			return ReaderInfo{}, errors.New("缺少百度文件 ID，请重新导入")
		}
		var result struct {
			List []struct {
				Dlink string `json:"dlink"`
				Path  string `json:"path"`
				Size  int64  `json:"size"`
			} `json:"list"`
		}
		if err := a.baiduAPI(ctx, s, "/xpan/multimedia", url.Values{"method": {"filemetas"}, "fsids": {"[" + f.SourceRemoteID + "]"}, "dlink": {"1"}}, &result); err != nil {
			return ReaderInfo{}, err
		}
		if len(result.List) != 1 || result.List[0].Dlink == "" {
			return ReaderInfo{}, errors.New("百度未提供下载地址，请检查文件权限与账号状态")
		}
		meta := result.List[0]
		if meta.Size != f.Size {
			return ReaderInfo{}, errors.New("来源文件大小已变更，请重新导入")
		}
		expected := path.Join(path.Clean("/"+strings.TrimPrefix(s.Root, "/")), strings.TrimPrefix(f.SourceKey, "/"))
		if meta.Path != "" && meta.Path != expected {
			return ReaderInfo{}, errors.New("百度文件路径已变更，请重新导入")
		}
		latest, _ := a.source(s.ID)
		c, err := a.credentials(latest)
		if err != nil {
			return ReaderInfo{}, err
		}
		u, err := url.Parse(meta.Dlink)
		if err != nil || u.Scheme != "https" {
			return ReaderInfo{}, errors.New("百度返回了无效的下载地址")
		}
		q := u.Query()
		q.Set("access_token", c.AccessToken)
		u.RawQuery = q.Encode()
		remote = u.String()
		headers.Set("User-Agent", "pan.baidu.com")
	default:
		return ReaderInfo{}, errors.New("不支持的下载来源")
	}
	// Unvalidated partial content is restarted rather than combined with a potentially changed file.
	validator := t.ETag
	if f.Size > 0 && offset >= f.Size {
		offset = 0
	}
	if strings.HasPrefix(validator, "W/") {
		validator = ""
	}
	if validator == "" {
		validator = t.LastModified
	}
	if offset > 0 && validator == "" {
		offset = 0
	}
	req, err := http.NewRequestWithContext(ctx, "GET", remote, nil)
	if err != nil {
		return ReaderInfo{}, err
	}
	req.Header = headers
	req.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		req.Header.Set("If-Range", validator)
	}
	client := *a.client
	client.Timeout = 0
	if client.Transport == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.ResponseHeaderTimeout = 45 * time.Second
		client.Transport = transport
	}
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) > 8 {
			return errors.New("下载地址重定向过多")
		}
		if next.URL.Scheme != "http" && next.URL.Scheme != "https" {
			return errors.New("无效重定向")
		}
		if via[0].URL.Scheme == "https" && next.URL.Scheme != "https" {
			return errors.New("拒绝将加密连接降级")
		}
		if next.URL.Host != via[0].URL.Host {
			next.Header.Del("Authorization")
			next.Header.Del("Cookie")
		}
		if s.Type == "quark" {
			if !validQuarkDownloadURL(next.URL.String()) {
				return errors.New("夸克下载地址跳转到了不支持的服务")
			}
			next.Header.Set("Cookie", headers.Get("Cookie"))
		}
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return ReaderInfo{}, errors.New("下载连接失败或已中断，可点击重试")
	}
	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		resp.Body.Close()
		return ReaderInfo{}, fmt.Errorf("下载失败（HTTP %d），请检查授权或重试", resp.StatusCode)
	}
	if s.Type == "quark" && strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
		resp.Body.Close()
		return ReaderInfo{}, errors.New("夸克返回了网页而非视频，请更新 Cookie 并检查下载权限")
	}
	total := resp.ContentLength
	if resp.StatusCode == 206 {
		var start, end, full int64
		if _, err = fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &full); err != nil || start != offset || full < 0 || end < start || end >= full {
			resp.Body.Close()
			return ReaderInfo{}, errors.New("远程服务返回的断点范围无效")
		}
		if t.ETag != "" && resp.Header.Get("ETag") != "" && t.ETag != resp.Header.Get("ETag") {
			resp.Body.Close()
			return ReaderInfo{}, errors.New("远程文件已变更，无法安全续传，请重新导入")
		}
		total = full
	} else {
		offset = 0
	}
	if f.Size > 0 && total > 0 && f.Size != total {
		resp.Body.Close()
		return ReaderInfo{}, errors.New("远程文件大小已变更，请重新导入")
	}
	if total < 0 {
		total = f.Size
	}
	return ReaderInfo{Reader: resp.Body, Total: total, Offset: offset, ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified")}, nil
}
