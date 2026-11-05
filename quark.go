package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const quarkAPIBase = "https://drive.quark.cn/1/clouddrive"
const quarkReferer = "https://pan.quark.cn/"
const quarkUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) quark-cloud-drive/2.5.20 Chrome/100.0.4896.160 Electron/18.3.5.4-b478491100 Safari/537.36 Channel/pckk_other_ch"

var quarkIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func validateQuarkCookie(raw string) error {
	if len(raw) == 0 || len(raw) > 32768 || strings.ContainsAny(raw, "\r\n\x00") {
		return errors.New("请填写夸克网页版请求头中的 Cookie 值，不能包含换行")
	}
	req := &http.Request{Header: http.Header{"Cookie": {raw}}}
	for _, c := range req.Cookies() {
		if (c.Name == "__puus" || c.Name == "__pus") && c.Value != "" {
			return nil
		}
	}
	return errors.New("Cookie 中没有夸克登录态，请登录夸克网页版后重新复制完整 Cookie 值")
}

func rotatedQuarkCookie(raw string, updates []*http.Cookie) string {
	req := &http.Request{Header: http.Header{"Cookie": {raw}}}
	cookies := req.Cookies()
	changed := false
	for _, update := range updates {
		if (update.Name != "__puus" && update.Name != "__pus") || update.Value == "" || update.MaxAge < 0 {
			continue
		}
		found := false
		for _, c := range cookies {
			if c.Name == update.Name {
				if c.Value != update.Value {
					c.Value, changed = update.Value, true
				}
				found = true
				break
			}
		}
		if !found {
			cookies = append(cookies, &http.Cookie{Name: update.Name, Value: update.Value})
			changed = true
		}
	}
	if !changed {
		return raw
	}
	parts := []string{}
	for _, c := range cookies {
		parts = append(parts, c.String())
	}
	return strings.Join(parts, "; ")
}

func (a *App) quarkAPI(ctx context.Context, s *Source, endpoint, method string, q url.Values, body any, out any) error {
	a.quarkMu.Lock()
	defer a.quarkMu.Unlock()
	if !s.Pending {
		if latest, e := a.source(s.ID); e == nil && latest.Type == "quark" && latest.Root == s.Root {
			s.Secret = latest.Secret
		}
	}
	c, err := a.credentials(*s)
	if err != nil {
		return errors.New("无法读取夸克登录态，请编辑来源重新填写 Cookie")
	}
	if err := validateQuarkCookie(c.Cookie); err != nil {
		return err
	}
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return errors.New("无法生成夸克请求")
		}
		payload = bytes.NewReader(data)
	}
	if q == nil {
		q = url.Values{}
	}
	q.Set("pr", "ucpro")
	q.Set("fr", "pc")
	req, err := http.NewRequestWithContext(ctx, method, quarkAPIBase+endpoint+"?"+q.Encode(), payload)
	if err != nil {
		return errors.New("无法生成夸克请求")
	}
	req.Header.Set("Cookie", c.Cookie)
	req.Header.Set("Referer", quarkReferer)
	req.Header.Set("Origin", "https://pan.quark.cn")
	req.Header.Set("User-Agent", quarkUserAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := *a.client
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("夸克网盘无法连接，请检查网络后重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return errors.New("夸克登录态或访问权限无效，请更新 Cookie 并检查账号权限")
	}
	if resp.StatusCode == 429 {
		return errors.New("夸克请求过于频繁，请稍后重试")
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("夸克接口返回 HTTP %d，请检查登录态或稍后重试", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (12<<20)+1))
	if err != nil || len(data) > 12<<20 {
		return errors.New("夸克接口响应不完整或过大，请重试")
	}
	var envelope struct {
		Status  *int   `json:"status"`
		Code    *int   `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Status == nil || envelope.Code == nil {
		return errors.New("夸克返回的数据格式不支持，登录态可能已过期，请更新 Cookie")
	}
	if *envelope.Code != 0 || *envelope.Status < 200 || *envelope.Status >= 300 {
		msg := strings.ToLower(envelope.Message)
		if strings.Contains(msg, "登录") || strings.Contains(msg, "login") || strings.Contains(msg, "auth") {
			return errors.New("夸克登录态已失效，请在来源设置中更新 Cookie")
		}
		return fmt.Errorf("夸克接口返回错误（状态 %d，代码 %d），请检查目录权限、账号下载权限或稍后重试", *envelope.Status, *envelope.Code)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New("夸克目录或下载数据格式不支持")
	}
	updated := rotatedQuarkCookie(c.Cookie, resp.Cookies())
	if updated != c.Cookie {
		oldSecret := s.Secret
		c.Cookie = updated
		secret, err := a.encrypt(c)
		if err != nil {
			return errors.New("无法保存夸克更新的登录态")
		}
		s.Secret = secret
		if !s.Pending {
			a.mu.Lock()
			for i := range a.s.Sources {
				if a.s.Sources[i].ID == s.ID && a.s.Sources[i].Secret == oldSecret {
					a.s.Sources[i].Secret = secret
					err = a.saveLocked()
					break
				}
			}
			a.mu.Unlock()
			if err != nil {
				return errors.New("无法保存夸克更新的登录态")
			}
		}
	}
	return nil
}

type quarkFile struct {
	ID            string `json:"fid"`
	Name          string `json:"file_name"`
	File          *bool  `json:"file"`
	Size          int64  `json:"size"`
	Modified      int64  `json:"updated_at"`
	LocalModified int64  `json:"l_updated_at"`
}

func (a *App) quarkChildren(ctx context.Context, s *Source, parent string) ([]quarkFile, error) {
	files := []quarkFile{}
	seen := map[string]bool{}
	for page := 1; page <= 100; page++ {
		var response struct {
			Data *struct {
				List []quarkFile `json:"list"`
			} `json:"data"`
			Metadata struct {
				Total *int `json:"_total"`
			} `json:"metadata"`
		}
		q := url.Values{"pdir_fid": {parent}, "_page": {strconv.Itoa(page)}, "_size": {"100"}, "_fetch_total": {"1"}, "_sort": {"file_type:asc,file_name:asc"}}
		if err := a.quarkAPI(ctx, s, "/file/sort", "GET", q, nil, &response); err != nil {
			return nil, err
		}
		if response.Data == nil || response.Data.List == nil {
			return nil, errors.New("夸克接口缺少目录内容，请重新登录后重试")
		}
		for _, f := range response.Data.List {
			f.Name = html.UnescapeString(f.Name)
			if !quarkIDRE.MatchString(f.ID) || f.Name == "" || f.Name == "." || f.Name == ".." || strings.ContainsAny(f.Name, "/\\\x00") || f.File == nil || f.Size < 0 || seen[f.ID] {
				return nil, errors.New("夸克返回了无效或重复的目录条目，请刷新重试")
			}
			seen[f.ID] = true
			files = append(files, f)
		}
		if response.Metadata.Total != nil && *response.Metadata.Total < len(files) {
			return nil, errors.New("夸克目录总数无效")
		}
		if response.Metadata.Total != nil && len(files) >= *response.Metadata.Total {
			return files, nil
		}
		if len(response.Data.List) < 100 {
			if response.Metadata.Total != nil && len(files) < *response.Metadata.Total {
				return nil, errors.New("夸克目录读取不完整，请刷新重试")
			}
			return files, nil
		}
	}
	return nil, errors.New("夸克目录超过 10000 个条目，请选择较小的子目录")
}

func (a *App) listQuark(ctx context.Context, s *Source, key string) ([]Entry, error) {
	k, err := cleanKey(key)
	if err != nil {
		return nil, err
	}
	if len(k) > 4096 || strings.Count(k, "/") > 24 {
		return nil, errors.New("夸克目录层级过深或路径过长")
	}
	parent := s.Root
	if parent == "" || parent == "/" {
		parent = "0"
	}
	if !quarkIDRE.MatchString(parent) {
		return nil, errors.New("夸克根文件夹 ID 无效")
	}
	if k != "/" {
		for _, name := range strings.Split(strings.Trim(k, "/"), "/") {
			children, err := a.quarkChildren(ctx, s, parent)
			if err != nil {
				return nil, err
			}
			found := ""
			for _, f := range children {
				if !*f.File && f.Name == name {
					if found != "" {
						return nil, errors.New("夸克目录有同名文件夹，无法确定路径")
					}
					found = f.ID
				}
			}
			if found == "" {
				return nil, errors.New("夸克目录不存在或已移动，请刷新目录")
			}
			parent = found
		}
	}
	files, err := a.quarkChildren(ctx, s, parent)
	if err != nil {
		return nil, err
	}
	entries := []Entry{}
	for _, f := range files {
		modified := ""
		stamp := f.Modified
		if stamp == 0 {
			stamp = f.LocalModified
		}
		if stamp > 0 {
			modified = time.UnixMilli(stamp).UTC().Format(time.RFC3339Nano)
		}
		entries = append(entries, Entry{Key: path.Join(k, f.Name), Name: f.Name, RemoteID: f.ID, Dir: !*f.File, Size: f.Size, Modified: modified, Media: *f.File && mediaExt(f.Name)})
	}
	return entries, nil
}

func validQuarkDownloadURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Port() != "" && u.Port() != "443" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, domain := range []string{"quark.cn", "qk-cdn.cn", "uc.cn"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func (a *App) quarkDownload(ctx context.Context, s Source, f MediaFile, t Task) (string, http.Header, error) {
	entry, err := a.inspectEntry(ctx, s, Entry{Key: f.SourceKey})
	if err != nil {
		return "", nil, err
	}
	if f.SourceRemoteID == "" || entry.RemoteID != f.SourceRemoteID || entry.Size != f.Size || t.SourceModified != "" && entry.Modified != t.SourceModified {
		return "", nil, errors.New("夸克来源文件已变更，请重新扫描并导入")
	}
	var response struct {
		Data []struct {
			ID   string `json:"fid"`
			Size int64  `json:"size"`
			URL  string `json:"download_url"`
		} `json:"data"`
	}
	if err := a.quarkAPI(ctx, &s, "/file/download", "POST", nil, map[string]any{"fids": []string{entry.RemoteID}}, &response); err != nil {
		return "", nil, err
	}
	if len(response.Data) != 1 || !validQuarkDownloadURL(response.Data[0].URL) {
		return "", nil, errors.New("夸克未提供受支持的原文件下载地址，请检查账号下载权限或稍后重试")
	}
	data := response.Data[0]
	if data.ID != "" && data.ID != entry.RemoteID || data.Size > 0 && data.Size != entry.Size {
		return "", nil, errors.New("夸克返回的下载文件与所选文件不一致，请重新扫描")
	}
	c, err := a.credentials(s)
	if err != nil {
		return "", nil, errors.New("无法读取夸克登录态")
	}
	headers := http.Header{}
	headers.Set("Cookie", c.Cookie)
	headers.Set("Referer", quarkReferer)
	headers.Set("User-Agent", quarkUserAgent)
	return data.URL, headers, nil
}
