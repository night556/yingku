package yingku

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type DLNADevice struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Manufacturer string    `json:"manufacturer"`
	Model        string    `json:"model"`
	IP           string    `json:"-"`
	AVURL        string    `json:"-"`
	AVType       string    `json:"-"`
	RenderURL    string    `json:"-"`
	RenderType   string    `json:"-"`
	Seen         time.Time `json:"-"`
}

type DLNADiscoverer func(context.Context) ([]DLNADevice, error)

type CastSession struct {
	ID                  string       `json:"id"`
	DeviceID            string       `json:"deviceId"`
	DeviceName          string       `json:"deviceName"`
	FileID              string       `json:"fileId"`
	Name                string       `json:"name"`
	Status              string       `json:"status"`
	Position            float64      `json:"position"`
	Duration            float64      `json:"duration"`
	Warning             string       `json:"warning,omitempty"`
	Created             time.Time    `json:"created"`
	Series              bool         `json:"series"`
	ItemID              string       `json:"itemId,omitempty"`
	AutoNext            bool         `json:"autoNext"`
	Previous            *CastEpisode `json:"previous,omitempty"`
	Next                *CastEpisode `json:"next,omitempty"`
	NextOnDevice        bool         `json:"nextOnDevice"`
	Current             *CastEpisode `json:"current,omitempty"`
	device              DLNADevice
	token               string
	lastSaved           time.Time
	progressToken       string
	mediaURL            string
	baseURL             string
	nextGrant           *castGrant
	pendingGrant        *castGrant
	queueTried          bool
	queueSupported      bool
	ended               bool
	observedPlaying     bool
	playingProgress     bool
	uriConfirmed        bool
	lastPlayingPosition float64
	lastPlayingAt       time.Time
	terminalSince       time.Time
	terminalReached     bool
}

type upnpService struct {
	Type    string `xml:"serviceType"`
	Control string `xml:"controlURL"`
}
type upnpDevice struct {
	Type         string        `xml:"deviceType"`
	Name         string        `xml:"friendlyName"`
	Manufacturer string        `xml:"manufacturer"`
	Model        string        `xml:"modelName"`
	UDN          string        `xml:"UDN"`
	Services     []upnpService `xml:"serviceList>service"`
	Devices      []upnpDevice  `xml:"deviceList>device"`
}

func deviceHTTP() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	return &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func deviceURL(raw string, peer net.IP) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || peer == nil {
		return nil, errors.New("无效的电视设备地址")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.Equal(peer) {
		return nil, errors.New("电视设备返回了其他主机的地址")
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return nil, errors.New("无效的设备端口")
		}
	}
	return u, nil
}

func readDevice(ctx context.Context, location string, peer net.IP) (DLNADevice, error) {
	base, err := deviceURL(location, peer)
	if err != nil {
		return DLNADevice{}, err
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", base.String(), nil)
	response, err := deviceHTTP().Do(req)
	if err != nil {
		return DLNADevice{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return DLNADevice{}, errors.New("无法读取电视设备描述")
	}
	var root struct {
		Base   string     `xml:"URLBase"`
		Device upnpDevice `xml:"device"`
	}
	if err = xml.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&root); err != nil {
		return DLNADevice{}, err
	}
	if root.Base != "" {
		base, err = deviceURL(strings.TrimSpace(root.Base), peer)
		if err != nil {
			return DLNADevice{}, err
		}
	}
	var find func(upnpDevice) (upnpDevice, bool)
	find = func(d upnpDevice) (upnpDevice, bool) {
		if strings.HasPrefix(d.Type, "urn:schemas-upnp-org:device:MediaRenderer:") {
			return d, true
		}
		for _, child := range d.Devices {
			if found, ok := find(child); ok {
				return found, true
			}
		}
		return upnpDevice{}, false
	}
	d, ok := find(root.Device)
	if !ok {
		return DLNADevice{}, errors.New("此设备不支持 DLNA 视频接收")
	}
	result := DLNADevice{Name: strings.TrimSpace(d.Name), Manufacturer: d.Manufacturer, Model: d.Model, IP: peer.String(), Seen: time.Now()}
	if len(result.Name) > 240 {
		result.Name = result.Name[:240]
	}
	if result.Name == "" {
		result.Name = "DLNA 设备 " + peer.String()
	}
	for _, service := range d.Services {
		av := strings.HasPrefix(service.Type, "urn:schemas-upnp-org:service:AVTransport:")
		render := strings.HasPrefix(service.Type, "urn:schemas-upnp-org:service:RenderingControl:")
		if !av && !render {
			continue
		}
		ref, err := url.Parse(strings.TrimSpace(service.Control))
		if err != nil {
			continue
		}
		control, err := deviceURL(base.ResolveReference(ref).String(), peer)
		if err != nil {
			return DLNADevice{}, err
		}
		if av {
			result.AVURL, result.AVType = control.String(), service.Type
		}
		if render {
			result.RenderURL, result.RenderType = control.String(), service.Type
		}
	}
	if result.AVURL == "" {
		return DLNADevice{}, errors.New("设备没有可用的播放控制服务")
	}
	sum := sha256.Sum256([]byte(peer.String() + "\x00" + d.UDN + "\x00" + result.AVURL))
	result.ID = hex.EncodeToString(sum[:16])
	return result, nil
}

func ssdpLocation(packet []byte, peer net.IP) (string, bool) {
	response, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(packet)), nil)
	if err != nil {
		return "", false
	}
	if response.Body != nil {
		_ = response.Body.Close()
	}
	if response.StatusCode != 200 {
		return "", false
	}
	location := response.Header.Get("Location")
	_, err = deviceURL(location, peer)
	return location, err == nil
}

func discoverDLNA(ctx context.Context) ([]DLNADevice, error) {
	ips := lanIPs()
	if len(ips) == 0 {
		return nil, errors.New("没有可用的局域网 IPv4 地址，请连接家中的网络")
	}
	if len(ips) > 8 {
		ips = ips[:8]
	}
	type target struct {
		location string
		peer     net.IP
	}
	var mu sync.Mutex
	targets := map[string]target{}
	var searches sync.WaitGroup
	for _, ip := range ips {
		searches.Add(1)
		go func(ip net.IP) {
			defer searches.Done()
			conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip})
			if err != nil {
				return
			}
			defer conn.Close()
			done := make(chan struct{})
			defer close(done)
			go func() {
				select {
				case <-ctx.Done():
					_ = conn.Close()
				case <-done:
				}
			}()
			_ = conn.SetDeadline(time.Now().Add(2800 * time.Millisecond))
			message := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: urn:schemas-upnp-org:device:MediaRenderer:1\r\n\r\n"
			destination := &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}
			_, _ = conn.WriteToUDP([]byte(message), destination)
			_, _ = conn.WriteToUDP([]byte(message), destination)
			buffer := make([]byte, 8192)
			for count := 0; count < 256; count++ {
				n, peer, err := conn.ReadFromUDP(buffer)
				if err != nil {
					return
				}
				if peer.IP.IsLoopback() || !(peer.IP.IsPrivate() || peer.IP.IsLinkLocalUnicast()) {
					continue
				}
				if location, ok := ssdpLocation(buffer[:n], peer.IP); ok {
					mu.Lock()
					if len(targets) < 64 {
						targets[location] = target{location, append(net.IP{}, peer.IP...)}
					}
					mu.Unlock()
				}
			}
		}(ip)
	}
	searches.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := []DLNADevice{}
	limit := make(chan struct{}, 4)
	var reads sync.WaitGroup
	for _, candidate := range targets {
		reads.Add(1)
		go func(candidate target) {
			defer reads.Done()
			select {
			case limit <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-limit }()
			if d, err := readDevice(ctx, candidate.location, candidate.peer); err == nil {
				mu.Lock()
				result = append(result, d)
				mu.Unlock()
			}
		}(candidate)
	}
	reads.Wait()
	sort.Slice(result, func(i, j int) bool { return naturalLess(result[i].Name, result[j].Name) })
	return result, nil
}

func xmlText(value string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}

func soap(ctx context.Context, d DLNADevice, rendering bool, action string, args [][2]string) (map[string]string, error) {
	address, kind := d.AVURL, d.AVType
	if rendering {
		address, kind = d.RenderURL, d.RenderType
	}
	if address == "" {
		return nil, errors.New("电视不支持此项控制")
	}
	if _, err := deviceURL(address, net.ParseIP(d.IP)); err != nil {
		return nil, err
	}
	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:` + action + ` xmlns:u="` + xmlText(kind) + `"><InstanceID>0</InstanceID>`)
	for _, arg := range args {
		body.WriteString("<" + arg[0] + ">" + xmlText(arg[1]) + "</" + arg[0] + ">")
	}
	body.WriteString("</u:" + action + "></s:Body></s:Envelope>")
	req, err := http.NewRequestWithContext(ctx, "POST", address, strings.NewReader(body.String()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", `"`+kind+"#"+action+`"`)
	response, err := deviceHTTP().Do(req)
	if err != nil {
		return nil, errors.New("无法连接电视，请确认设备在线并处于同一局域网")
	}
	defer response.Body.Close()
	decoder := xml.NewDecoder(io.LimitReader(response.Body, 1<<20))
	values := map[string]string{}
	var stack []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("电视返回了无法识别的响应")
		}
		switch token := token.(type) {
		case xml.StartElement:
			stack = append(stack, token.Name.Local)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				key := stack[len(stack)-1]
				values[key] += string(token)
			}
		}
	}
	for k, v := range values {
		values[k] = strings.TrimSpace(v)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || values["errorCode"] != "" {
		message := map[string]string{"401": "电视不支持此项控制", "701": "电视当前无法执行操作，请先停止原来的播放", "710": "电视不支持这种拖动进度方式", "711": "电视无法跳转到此位置", "714": "电视不支持当前视频格式", "716": "电视无法读取视频，请检查网络连接及视频格式"}
		if m := message[values["errorCode"]]; m != "" {
			return nil, errors.New(m)
		}
		return nil, errors.New("电视未接受操作，可能不支持此功能或视频格式")
	}
	return values, nil
}

func mediaType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".ts":
		return "video/mp2t"
	case ".avi":
		return "video/x-msvideo"
	default:
		return "application/octet-stream"
	}
}
func upnpTime(seconds float64) string {
	n := int(seconds)
	if n < 0 {
		n = 0
	}
	return fmt.Sprintf("%02d:%02d:%02d", n/3600, (n/60)%60, n%60)
}
func parseUPnPTime(raw string) (float64, bool) {
	parts := strings.Split(raw, ":")
	if len(parts) != 3 {
		return 0, false
	}
	h, e1 := strconv.Atoi(parts[0])
	m, e2 := strconv.Atoi(parts[1])
	s, e3 := strconv.ParseFloat(parts[2], 64)
	if e1 != nil || e2 != nil || e3 != nil || h < 0 || h > 9999 || m < 0 || m >= 60 || s < 0 || s >= 60 || math.IsNaN(s) || math.IsInf(s, 0) {
		return 0, false
	}
	return float64(h*3600+m*60) + s, true
}
func (a *App) castSnapshotLocked(s *CastSession) CastSession {
	result := *s
	result.Series, result.ItemID, result.Previous, result.Next = a.castNeighborsLocked(s.FileID)
	if result.Series {
		for _, file := range a.s.Files {
			if file.ID == s.FileID {
				result.Current = &CastEpisode{ID: file.ID, Name: file.Name, Season: file.Season, Episode: file.Episode, EpisodeEnd: file.EpisodeEnd, Ready: file.Status == "completed"}
				break
			}
		}
	}
	result.NextOnDevice = s.nextGrant != nil && s.queueSupported
	return result
}
func (a *App) lanReady() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return listenerAllowsLAN(a.listenAddress)
}

func (a *App) startCast(ctx context.Context, deviceID, fileID string, position *float64) (CastSession, error) {
	return a.startCastWithAutoNext(ctx, deviceID, fileID, position, nil)
}

func (a *App) startCastWithAutoNext(ctx context.Context, deviceID, fileID string, position *float64, autoNext *bool) (CastSession, error) {
	a.dlnaMu.Lock()
	defer a.dlnaMu.Unlock()
	if !a.lanReady() {
		return CastSession{}, errors.New("当前服务仅监听本机，请使用 0.0.0.0 的监听地址重启")
	}
	a.mu.Lock()
	device, ok := a.devices[deviceID]
	port := listenerPort(a.listenAddress)
	baseURL := a.s.Config.CastBaseURL
	a.mu.Unlock()
	if !ok || time.Since(device.Seen) > 10*time.Minute {
		return CastSession{}, errors.New("设备记录已过期，请重新搜索电视")
	}
	file, err := a.file(fileID)
	if err != nil || file.Status != "completed" {
		return CastSession{}, errors.New("视频尚未准备好")
	}
	info, err := os.Stat(file.Destination)
	if err != nil || !info.Mode().IsRegular() {
		return CastSession{}, errors.New("视频文件已移动或无法读取")
	}
	if baseURL == "" {
		conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP(device.IP), Port: 9})
		if err != nil {
			return CastSession{}, errors.New("无法确定通往电视的局域网地址")
		}
		localIP := conn.LocalAddr().(*net.UDPAddr).IP
		_ = conn.Close()
		baseURL = "http://" + net.JoinHostPort(localIP.String(), strconv.Itoa(port))
	}
	a.mu.Lock()
	series, _, _, _ := a.castNeighborsLocked(file.ID)
	a.mu.Unlock()
	enabled := series && (autoNext == nil || *autoNext)
	s := &CastSession{ID: uid(), DeviceID: device.ID, DeviceName: device.Name, FileID: file.ID, Name: file.Name, Status: "TRANSITIONING", Created: time.Now(), device: device, token: uid() + uid(), Position: file.Position, Duration: file.Duration, AutoNext: enabled, baseURL: baseURL}
	if position != nil {
		s.Position = *position
	}
	if s.Position < 0 || s.Position > 35999999 {
		return CastSession{}, errors.New("无效的播放位置")
	}
	address := baseURL + "/cast/" + s.token + "/video" + strings.ToLower(filepath.Ext(file.Name))
	s.mediaURL = address
	metadata := castMetadata(file, address)
	a.mu.Lock()
	var replaced []*CastSession
	for _, old := range a.casts {
		if old.DeviceID == device.ID {
			replaced = append(replaced, old)
		}
	}
	a.mu.Unlock()
	for _, old := range replaced {
		if err := a.clearCastQueue(ctx, old); err != nil {
			return CastSession{}, err
		}
	}
	a.mu.Lock()
	if a.casts == nil {
		a.casts = map[string]*CastSession{}
	}
	for id, old := range a.casts {
		if time.Since(old.Created) > 24*time.Hour || old.DeviceID == device.ID || old.Status == "STOPPED" {
			delete(a.casts, id)
		}
	}
	if len(a.casts) >= 16 {
		a.mu.Unlock()
		return CastSession{}, errors.New("同时投屏的设备过多，请先停止已有投屏")
	}
	a.casts[s.ID] = s
	a.mu.Unlock()
	_, err = soap(ctx, device, false, "SetAVTransportURI", [][2]string{{"CurrentURI", address}, {"CurrentURIMetaData", metadata}})
	if err == nil {
		_, err = soap(ctx, device, false, "Play", [][2]string{{"Speed", "1"}})
	}
	if err != nil {
		a.mu.Lock()
		delete(a.casts, s.ID)
		a.mu.Unlock()
		return CastSession{}, err
	}
	warning := ""
	if s.Position > 5 {
		if _, e := soap(ctx, device, false, "Seek", [][2]string{{"Unit", "REL_TIME"}, {"Target", upnpTime(s.Position)}}); e != nil {
			warning = "已开始播放，电视暂未接受续播位置"
		}
	}
	a.mu.Lock()
	s.Status, s.Warning = "PLAYING", warning
	a.claimCastProgressLocked(s)
	a.mu.Unlock()
	_ = a.syncCastQueue(ctx, s)
	a.mu.Lock()
	result := a.castSnapshotLocked(s)
	a.mu.Unlock()
	go a.monitorCast(s.ID)
	return result, nil
}

func (a *App) monitorCast(id string) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		a.mu.Lock()
		s := a.casts[id]
		ended := a.closing || s == nil || s.ended || time.Since(s.Created) > 24*time.Hour || a.listenAddress == ""
		a.mu.Unlock()
		if ended {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		_, _ = a.castStatus(ctx, id)
		cancel()
	}
}

func (a *App) castStatus(ctx context.Context, id string) (CastSession, error) {
	a.dlnaMu.Lock()
	defer a.dlnaMu.Unlock()
	return a.castStatusLocked(ctx, id)
}
func (a *App) castStatusLocked(ctx context.Context, id string) (CastSession, error) {
	a.mu.Lock()
	s := a.casts[id]
	if s == nil {
		a.mu.Unlock()
		return CastSession{}, errors.New("投屏已结束，请重新选择电视")
	}
	snapshot := a.castSnapshotLocked(s)
	a.mu.Unlock()
	if snapshot.ended {
		return snapshot, nil
	}
	transport, e1 := soap(ctx, snapshot.device, false, "GetTransportInfo", nil)
	position, e2 := soap(ctx, snapshot.device, false, "GetPositionInfo", nil)
	a.mu.Lock()
	if e1 != nil || e2 != nil {
		s.Warning = "暂时无法获取电视状态，请检查设备连接"
		result := a.castSnapshotLocked(s)
		a.mu.Unlock()
		return result, nil
	}
	s.Warning = ""
	uri := position["TrackURI"]
	promoted := false
	if grant := s.nextGrant; grant != nil && uri == grant.address {
		_, _, _, next := a.castNeighborsLocked(s.FileID)
		if !s.AutoNext || next == nil || !next.Ready || next.ID != grant.fileID {
			s.ended = true
			s.Status = "STOPPED"
			s.token = ""
			s.nextGrant = nil
			s.Warning = "剧集列表已变化，请重新选择视频投屏"
			result := a.castSnapshotLocked(s)
			a.mu.Unlock()
			return result, nil
		}
		if s.observedPlaying && s.Duration > 0 && s.Duration-s.lastPlayingPosition <= 8 && time.Since(s.lastPlayingAt).Seconds() >= s.Duration-s.lastPlayingPosition {
			s.Position = s.Duration
		}
		a.saveCastProgressLocked(s)
		s.FileID, s.token, s.mediaURL = grant.fileID, grant.token, grant.address
		a.claimCastProgressLocked(s)
		s.Name = next.Name
		s.Position = 0
		s.Duration = 0
		s.nextGrant = nil
		s.lastSaved = time.Time{}
		s.observedPlaying = false
		s.playingProgress = false
		s.terminalSince = time.Time{}
		promoted = true
	}
	s.uriConfirmed = uri != "" && uri == s.mediaURL
	if uri != "" && uri != "NOT_IMPLEMENTED" && !s.uriConfirmed {
		s.Status = "STOPPED"
		s.ended = true
		s.token = ""
		s.nextGrant = nil
		s.Warning = "电视已切换到其他内容"
		result := a.castSnapshotLocked(s)
		a.mu.Unlock()
		return result, nil
	}
	previousStatus := s.Status
	switch transport["CurrentTransportState"] {
	case "PLAYING", "PAUSED_PLAYBACK", "STOPPED", "TRANSITIONING", "NO_MEDIA_PRESENT":
		s.Status = transport["CurrentTransportState"]
	}
	if previousStatus == "PAUSED_PLAYBACK" && s.Status == "PLAYING" {
		for _, file := range a.s.Files {
			if file.ID == s.FileID && file.ProgressToken != s.progressToken {
				a.claimCastProgressLocked(s)
				break
			}
		}
	}
	if value, valid := parseUPnPTime(position["TrackDuration"]); valid && value > 0 {
		s.Duration = value
	}
	observedPosition, validPosition := parseUPnPTime(position["RelTime"])
	terminal := s.Status == "STOPPED" || s.Status == "NO_MEDIA_PRESENT"
	reached := terminal && castReachedEnd(s, observedPosition, validPosition, time.Now())
	if validPosition && (!terminal || observedPosition > 0) {
		s.Position = observedPosition
	}
	advanceID := ""
	if promoted && terminal {
		s.Status = "TRANSITIONING"
		terminal = false
	}
	if s.Status == "PLAYING" && validPosition {
		if s.observedPlaying && observedPosition > s.lastPlayingPosition {
			s.playingProgress = true
		}
		if !s.observedPlaying || observedPosition != s.lastPlayingPosition {
			s.lastPlayingAt = time.Now()
			s.lastPlayingPosition = observedPosition
		}
		s.observedPlaying = true
		s.terminalSince = time.Time{}
	} else if s.Status == "PAUSED_PLAYBACK" {
		s.observedPlaying = false
		s.playingProgress = false
		s.terminalSince = time.Time{}
	}
	if terminal {
		_, _, _, next := a.castNeighborsLocked(s.FileID)
		if s.terminalSince.IsZero() {
			s.terminalSince = time.Now()
			s.terminalReached = reached
		}
		reached = s.terminalReached
		if reached && s.Duration > 0 {
			s.Position = s.Duration
		}
		if s.nextGrant != nil && s.queueSupported && s.observedPlaying && s.Duration-s.lastPlayingPosition <= 8 && time.Since(s.terminalSince) < 8*time.Second {
			s.Status = "TRANSITIONING"
		} else if reached && s.AutoNext && next != nil && next.Ready {
			advanceID = next.ID
			s.Status = "TRANSITIONING"
		} else {
			s.ended = true
			s.token = ""
			if reached && s.AutoNext && next != nil && !next.Ready {
				s.Warning = "下一集尚未入库，连播已暂停"
			}
		}
	}
	if time.Since(s.lastSaved) > 10*time.Second || terminal || promoted {
		a.saveCastProgressLocked(s)
	}
	ended := s.ended
	nativeFallback := advanceID != "" && s.nextGrant != nil && s.queueSupported
	a.mu.Unlock()
	var queueErr error
	if advanceID != "" {
		if nativeFallback {
			queueErr = a.clearCastQueue(ctx, s)
			if queueErr == nil {
				a.mu.Lock()
				s.queueSupported = false
				s.queueTried = true
				a.mu.Unlock()
			}
		}
		if queueErr == nil {
			queueErr = a.switchCastEpisode(ctx, s, advanceID, true)
		}
		if queueErr != nil {
			a.mu.Lock()
			s.ended = true
			s.Status = "STOPPED"
			s.token = ""
			a.mu.Unlock()
		}
	} else if ended {
		queueErr = a.clearCastQueue(ctx, s)
		a.mu.Lock()
		s.nextGrant = nil
		a.mu.Unlock()
	} else {
		queueErr = a.syncCastQueue(ctx, s)
	}
	a.mu.Lock()
	if queueErr != nil {
		s.Warning = queueErr.Error()
	}
	result := a.castSnapshotLocked(s)
	a.mu.Unlock()
	return result, nil
}

func (a *App) saveCastProgressLocked(s *CastSession) {
	for i := range a.s.Files {
		f := &a.s.Files[i]
		if f.ID == s.FileID {
			if s.progressToken == "" || f.ProgressToken != s.progressToken {
				return
			}
			oldPosition, oldDuration := f.Position, f.Duration
			oldPlayed := f.LastPlayed
			f.Position = s.Position
			if s.Duration > 0 {
				f.Duration = s.Duration
			}
			if f.Position != oldPosition || f.Duration != oldDuration {
				f.LastPlayed = time.Now().UTC().Format(time.RFC3339Nano)
			}
			if err := a.saveLocked(); err != nil {
				f.Position, f.Duration = oldPosition, oldDuration
				f.LastPlayed = oldPlayed
				s.Warning = "电视正在播放，但进度暂未保存"
			} else {
				s.lastSaved = time.Now()
			}
			return
		}
	}
}

func (a *App) castControl(ctx context.Context, id, action string, value float64) (CastSession, error) {
	a.dlnaMu.Lock()
	defer a.dlnaMu.Unlock()
	a.mu.Lock()
	s := a.casts[id]
	if s == nil {
		a.mu.Unlock()
		return CastSession{}, errors.New("投屏已结束")
	}
	snapshot := a.castSnapshotLocked(s)
	a.mu.Unlock()
	if (snapshot.Status == "STOPPED" || snapshot.Status == "NO_MEDIA_PRESENT") && action != "stop" {
		return CastSession{}, errors.New("投屏已结束，请重新选择电视")
	}
	if action == "next" || action == "previous" {
		target := snapshot.Next
		if action == "previous" {
			target = snapshot.Previous
		}
		if target == nil {
			return CastSession{}, errors.New("已经没有相邻的剧集")
		}
		if !target.Ready {
			return CastSession{}, errors.New("这一集尚未入库完成")
		}
		if err := a.switchCastEpisode(ctx, s, target.ID, false); err != nil {
			return CastSession{}, err
		}
		a.mu.Lock()
		result := a.castSnapshotLocked(s)
		a.mu.Unlock()
		return result, nil
	}
	if action == "auto-next" {
		if !snapshot.Series || (value != 0 && value != 1) {
			return CastSession{}, errors.New("无效的连播设置")
		}
		a.mu.Lock()
		s.AutoNext = value == 1
		a.mu.Unlock()
		if err := a.syncCastQueue(ctx, s); err != nil {
			a.mu.Lock()
			s.AutoNext = snapshot.AutoNext
			a.mu.Unlock()
			return CastSession{}, err
		}
		a.mu.Lock()
		result := a.castSnapshotLocked(s)
		a.mu.Unlock()
		return result, nil
	}
	var args [][2]string
	operation, rendering := action, false
	switch action {
	case "play":
		operation = "Play"
		args = [][2]string{{"Speed", "1"}}
	case "pause":
		operation = "Pause"
	case "stop":
		_ = a.clearCastQueue(ctx, s)
		operation = "Stop"
	case "seek":
		if value < 0 || value > 35999999 {
			return CastSession{}, errors.New("无效的播放位置")
		}
		operation = "Seek"
		args = [][2]string{{"Unit", "REL_TIME"}, {"Target", upnpTime(value)}}
	case "volume":
		if value < 0 || value > 100 {
			return CastSession{}, errors.New("音量需要在 0 至 100 之间")
		}
		operation, rendering = "SetVolume", true
		args = [][2]string{{"Channel", "Master"}, {"DesiredVolume", strconv.Itoa(int(value))}}
	default:
		return CastSession{}, errors.New("不支持的投屏操作")
	}
	_, err := soap(ctx, snapshot.device, rendering, operation, args)
	if err != nil && action != "stop" {
		return CastSession{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s.Warning = ""
	if action == "play" || action == "seek" {
		for _, file := range a.s.Files {
			if file.ID == s.FileID && file.ProgressToken != s.progressToken {
				a.claimCastProgressLocked(s)
				break
			}
		}
	}
	switch action {
	case "play":
		s.Status = "PLAYING"
	case "pause":
		s.Status = "PAUSED_PLAYBACK"
		s.observedPlaying = false
		s.playingProgress = false
	case "seek":
		s.Position = value
		s.observedPlaying = false
		s.playingProgress = false
	case "stop":
		s.Status, s.token = "STOPPED", ""
		s.ended = true
		s.nextGrant = nil
		if err != nil {
			s.Warning = "已断开投屏，电视未响应停止指令"
		}
	}
	if action != "volume" {
		a.saveCastProgressLocked(s)
	}
	return a.castSnapshotLocked(s), nil
}

func (a *App) castMedia(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	a.mu.Lock()
	fileID := ""
	if a.listenAddress != "" {
		for _, s := range a.casts {
			if len(token) == 48 && time.Since(s.Created) < 24*time.Hour && !s.ended && s.Status != "STOPPED" {
				if s.token == token {
					fileID = s.FileID
					break
				}
				if s.nextGrant != nil && s.nextGrant.token == token {
					fileID = s.nextGrant.fileID
					break
				}
				if s.pendingGrant != nil && s.pendingGrant.token == token {
					fileID = s.pendingGrant.fileID
					break
				}
			}
		}
	}
	a.mu.Unlock()
	f, err := a.file(fileID)
	if err != nil || f.Status != "completed" {
		http.NotFound(w, r)
		return
	}
	info, err := os.Lstat(f.Destination)
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(f.Destination)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", mediaType(f.Name))
	w.Header().Set("transferMode.dlna.org", "Streaming")
	w.Header().Set("contentFeatures.dlna.org", "DLNA.ORG_OP=01;DLNA.ORG_CI=0")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, filepath.Base(f.Destination), info.ModTime(), file)
}

func (a *App) dlnaRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/dlna/sessions/{id}/sync", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !a.dlnaMu.TryLock() {
			a.mu.Lock()
			s := a.casts[id]
			if s == nil {
				a.mu.Unlock()
				fail(w, errors.New("投屏已结束"))
				return
			}
			result := a.castSnapshotLocked(s)
			a.mu.Unlock()
			jsonResponse(w, 200, result)
			return
		}
		defer a.dlnaMu.Unlock()
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		result, err := a.castStatusLocked(ctx, id)
		if err != nil {
			fail(w, err)
			return
		}
		a.mu.Lock()
		if s := a.casts[id]; s != nil {
			a.saveCastProgressLocked(s)
		}
		a.mu.Unlock()
		jsonResponse(w, 200, result)
	})
	mux.HandleFunc("GET /cast/{token}/{name}", a.castMedia)
	mux.HandleFunc("POST /api/dlna/discover", func(w http.ResponseWriter, r *http.Request) {
		if !a.lanReady() {
			fail(w, errors.New("当前服务仅监听本机，请使用 0.0.0.0 的监听地址重启"))
			return
		}
		if !a.dlnaMu.TryLock() {
			fail(w, errors.New("正在搜索或控制电视，请稍后再试"))
			return
		}
		defer a.dlnaMu.Unlock()
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		discover := discoverDLNA
		if a.discoverDevices != nil {
			discover = a.discoverDevices
		}
		devices, err := discover(ctx)
		if err != nil {
			fail(w, err)
			return
		}
		a.mu.Lock()
		if a.devices == nil {
			a.devices = map[string]DLNADevice{}
		}
		for id, d := range a.devices {
			if time.Since(d.Seen) > 10*time.Minute {
				delete(a.devices, id)
			}
		}
		for _, d := range devices {
			a.devices[d.ID] = d
		}
		a.mu.Unlock()
		jsonResponse(w, 200, map[string]any{"devices": devices})
	})
	mux.HandleFunc("POST /api/dlna/cast", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			DeviceID string   `json:"deviceId"`
			FileID   string   `json:"fileId"`
			Position *float64 `json:"position"`
			AutoNext *bool    `json:"autoNext"`
		}
		if err := readJSON(w, r, &body); err != nil {
			fail(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
		defer cancel()
		s, err := a.startCastWithAutoNext(ctx, body.DeviceID, body.FileID, body.Position, body.AutoNext)
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, 200, s)
	})
	mux.HandleFunc("GET /api/dlna/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		s := a.casts[r.PathValue("id")]
		if s == nil {
			a.mu.Unlock()
			fail(w, errors.New("投屏已结束，请重新选择电视"))
			return
		}
		result := a.castSnapshotLocked(s)
		a.mu.Unlock()
		jsonResponse(w, 200, result)
	})
	mux.HandleFunc("POST /api/dlna/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Action string  `json:"action"`
			Value  float64 `json:"value"`
		}
		if err := readJSON(w, r, &body); err != nil {
			fail(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		s, err := a.castControl(ctx, r.PathValue("id"), body.Action, body.Value)
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, 200, s)
	})
}
