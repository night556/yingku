package yingku

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockTV struct {
	mu           sync.Mutex
	URI          string
	Metadata     string
	Actions      []string
	Status       string
	Position     string
	Fault        string
	NextURI      string
	NextMetadata string
	SupportsNext bool
	ResetOnSet   bool
	Duration     string
	Faults       map[string]string
}

func tvFixture(t *testing.T) (*httptest.Server, *mockTV) {
	t.Helper()
	tv := &mockTV{Status: "PLAYING", Position: "00:12:34"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprint(w, `<root><device><deviceType>urn:schemas-upnp-org:device:MediaRenderer:1</deviceType><friendlyName>模拟客厅电视（仅接口测试）</friendlyName><manufacturer>Yingku QA</manufacturer><modelName>协议联调设备</modelName><UDN>uuid:yingku-mock-tv</UDN><serviceList><service><serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType><controlURL>/av/control</controlURL></service><service><serviceType>urn:schemas-upnp-org:service:RenderingControl:1</serviceType><controlURL>/render/control</controlURL></service></serviceList></device></root>`)
			return
		}
		action := r.Header.Get("SOAPAction")
		if !strings.HasPrefix(action, `"urn:schemas-upnp-org:service:`) || !strings.HasSuffix(action, `"`) {
			t.Error("invalid SOAPAction header")
		}
		action = strings.TrimSuffix(strings.Split(action, "#")[1], `"`)
		raw, _ := io.ReadAll(r.Body)
		var envelope struct {
			Body struct {
				Action struct {
					URI          string `xml:"CurrentURI"`
					Metadata     string `xml:"CurrentURIMetaData"`
					Target       string `xml:"Target"`
					NextURI      string `xml:"NextURI"`
					NextMetadata string `xml:"NextURIMetaData"`
				} `xml:",any"`
			} `xml:"Body"`
		}
		if err := xml.Unmarshal(raw, &envelope); err != nil {
			t.Error(err)
		}
		tv.mu.Lock()
		defer tv.mu.Unlock()
		tv.Actions = append(tv.Actions, action)
		fault := tv.Fault
		if value := tv.Faults[action]; value != "" {
			fault = value
		}
		if action == "SetNextAVTransportURI" && !tv.SupportsNext {
			fault = "401"
		}
		if action == "SetNextAVTransportURI" && envelope.Body.Action.NextURI == "" && tv.Faults["ClearNext"] != "" {
			fault = tv.Faults["ClearNext"]
		}
		if fault != "" {
			w.WriteHeader(500)
			fmt.Fprint(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><detail><UPnPError><errorCode>`+fault+`</errorCode><errorDescription>untrusted-error-secret</errorDescription></UPnPError></detail></s:Fault></s:Body></s:Envelope>`)
			return
		}
		value := ""
		switch action {
		case "SetAVTransportURI":
			tv.URI, tv.Metadata = envelope.Body.Action.URI, envelope.Body.Action.Metadata
			if tv.ResetOnSet {
				tv.Position = "00:00:00"
			}
		case "SetNextAVTransportURI":
			tv.NextURI, tv.NextMetadata = envelope.Body.Action.NextURI, envelope.Body.Action.NextMetadata
		case "Play":
			tv.Status = "PLAYING"
		case "Pause":
			tv.Status = "PAUSED_PLAYBACK"
		case "Stop":
			tv.Status = "STOPPED"
		case "Seek":
			tv.Position = envelope.Body.Action.Target
		case "GetTransportInfo":
			value = "<CurrentTransportState>" + tv.Status + "</CurrentTransportState>"
		case "GetPositionInfo":
			duration := tv.Duration
			if duration == "" {
				duration = "01:00:00"
			}
			value = "<RelTime>" + tv.Position + "</RelTime><TrackDuration>" + duration + "</TrackDuration><TrackURI>" + xmlText(tv.URI) + "</TrackURI>"
		}
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:`+action+`Response xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">`+value+"</u:"+action+"Response></s:Body></s:Envelope>")
	}))
	t.Cleanup(server.Close)
	return server, tv
}

func castingFixture(t *testing.T) (*App, *httptest.Server, *mockTV, DLNADevice) {
	t.Helper()
	a := testApp(t)
	configureTestPassword(t, a)
	file := filepath.Join(t.TempDir(), "样片 & 电视.mp4")
	if sample := os.Getenv("YINGKU_QA_VIDEO"); sample != "" {
		b, err := os.ReadFile(sample)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(file, b, 0600); err != nil {
			t.Fatal(err)
		}
	} else if err := os.WriteFile(file, []byte("0123456789-video-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	a.s.Items = []Item{{ID: "cast-movie", Title: "投屏流程联调（模拟电视）", Type: "movie", Year: "2026", MetadataStatus: "manual"}}
	a.s.Files = []MediaFile{{ID: "cast-file", ItemID: "cast-movie", Name: filepath.Base(file), Destination: file, Status: "completed", Reference: true}}
	server := httptest.NewServer(a.handler())
	t.Cleanup(server.Close)
	u, _ := url.Parse(server.URL)
	a.listenAddress = "0.0.0.0:" + u.Port()
	tvServer, tv := tvFixture(t)
	device, err := readDevice(context.Background(), tvServer.URL+"/description.xml", net.ParseIP("127.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	a.devices = map[string]DLNADevice{device.ID: device}
	a.discoverDevices = func(context.Context) ([]DLNADevice, error) { return []DLNADevice{device}, nil }
	t.Cleanup(a.closeNetwork)
	return a, server, tv, device
}

func TestDLNAStartStreamControlAndProgress(t *testing.T) {
	a, server, tv, d := castingFixture(t)
	s, err := a.startCast(context.Background(), d.ID, "cast-file", nil)
	if err != nil {
		t.Fatal(err)
	}
	tv.mu.Lock()
	uri, metadata := tv.URI, tv.Metadata
	tv.mu.Unlock()
	if !strings.HasPrefix(uri, server.URL+"/cast/") || strings.Contains(uri, "cast-file") || strings.Contains(uri, a.dir) {
		t.Fatal("cast URL is not scoped and opaque")
	}
	var didl struct {
		Item struct {
			Title string `xml:"title"`
		} `xml:"item"`
	}
	if err := xml.Unmarshal([]byte(metadata), &didl); err != nil || didl.Item.Title != "样片 & 电视.mp4" {
		t.Fatalf("invalid DIDL metadata: %v", err)
	}
	u, _ := url.Parse(uri)
	r := httptest.NewRequest("GET", "http://localhost"+u.RequestURI(), nil)
	r.RemoteAddr = "192.168.50.40:1234"
	r.Header.Set("Range", "bytes=2-5")
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != "2345" || w.Header().Get("Content-Range") == "" {
		t.Fatalf("TV cannot read ranged media: %d %s", w.Code, w.Body)
	}
	r = httptest.NewRequest("HEAD", "http://localhost"+u.RequestURI(), nil)
	r.RemoteAddr = "192.168.50.40:1234"
	w = httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("HEAD does not work for TV")
	}
	s, err = a.castStatus(context.Background(), s.ID)
	if err != nil || s.Position != 754 || s.Duration != 3600 {
		t.Fatalf("status parse: %+v %v", s, err)
	}
	file, _ := a.file("cast-file")
	if file.Position != 754 || file.Duration != 3600 {
		t.Fatal("TV playback progress not saved")
	}
	for _, control := range []struct {
		action string
		value  float64
	}{{"pause", 0}, {"seek", 125}, {"volume", 35}, {"play", 0}, {"stop", 0}} {
		s, err = a.castControl(context.Background(), s.ID, control.action, control.value)
		if err != nil {
			t.Fatalf("%s: %v", control.action, err)
		}
	}
	if s.Status != "STOPPED" {
		t.Fatal("stop failed")
	}
	response, err := http.Get(uri)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal("stopped cast still exposes video")
	}
	encoded, _ := json.Marshal(s)
	if strings.Contains(string(encoded), "/cast/") || strings.Contains(string(encoded), "/av/control") {
		t.Fatal("private control URL or TV grant leaked")
	}
}

func TestDLNAFaultAndDeviceAddressValidation(t *testing.T) {
	a, _, tv, d := castingFixture(t)
	tv.mu.Lock()
	tv.Fault = "714"
	tv.mu.Unlock()
	_, err := a.startCast(context.Background(), d.ID, "cast-file", nil)
	if err == nil || strings.Contains(err.Error(), "untrusted-error-secret") || len(a.casts) != 0 {
		t.Fatal("failed cast leaked response or left an active grant")
	}
	for _, address := range []string{"http://127.0.0.2:123/device", "http://example.com/device", "http://user:password@127.0.0.1/device", "file:///secret"} {
		if _, err := deviceURL(address, net.ParseIP("127.0.0.1")); err == nil {
			t.Fatalf("unsafe device address accepted: %s", address)
		}
	}
	for _, raw := range []string{"NOT_IMPLEMENTED", "00:00:NaN", "00:99:01", "-1:00:00"} {
		if _, ok := parseUPnPTime(raw); ok {
			t.Fatalf("invalid TV time accepted: %s", raw)
		}
	}
	packet := []byte("HTTP/1.1 200 OK\r\nLOCATION: http://192.168.1.20:123/description.xml\r\n\r\n")
	if _, ok := ssdpLocation(packet, net.ParseIP("192.168.1.20")); !ok {
		t.Fatal("valid SSDP location rejected")
	}
	if _, ok := ssdpLocation(packet, net.ParseIP("192.168.1.21")); ok {
		t.Fatal("SSDP peer mismatch accepted")
	}
}

func TestDLNAMonitorSavesProgressWithoutPhonePolling(t *testing.T) {
	a, _, _, device := castingFixture(t)
	if _, err := a.startCast(context.Background(), device.ID, "cast-file", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 8*time.Second, func() bool {
		f, err := a.file("cast-file")
		return err == nil && f.Position == 754 && f.Duration == 3600
	})
}

func TestDLNAInteractivePreview(t *testing.T) {
	if os.Getenv("YINGKU_DLNA_PREVIEW") != "1" {
		t.Skip("interactive UI fixture is opt-in")
	}
	fixture := castingFixture
	if os.Getenv("YINGKU_QA_CAST_SERIES") == "1" {
		fixture = func(t *testing.T) (*App, *httptest.Server, *mockTV, DLNADevice) { return castSeriesFixture(t, true) }
	}
	a, server, tv, _ := fixture(t)
	if os.Getenv("YINGKU_QA_TASKS") == "1" {
		addTaskPagePreview(t, a)
	}
	if os.Getenv("YINGKU_QA_CAST_SERIES") == "1" {
		tv.mu.Lock()
		tv.Duration = "00:00:15"
		tv.mu.Unlock()
		done := make(chan struct{})
		defer close(done)
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					tv.mu.Lock()
					if tv.Status == "PLAYING" && tv.URI != "" {
						value, _ := parseUPnPTime(tv.Position)
						value++
						tv.Position = upnpTime(value)
						if value >= 15 {
							if tv.NextURI != "" {
								tv.URI = tv.NextURI
								tv.Metadata = tv.NextMetadata
								tv.NextURI = ""
								tv.NextMetadata = ""
								tv.Position = "00:00:00"
							} else {
								tv.Status = "STOPPED"
								tv.Position = "00:00:00"
							}
						}
					}
					tv.mu.Unlock()
				}
			}
		}()
	}
	if os.Getenv("YINGKU_QA_NO_PASSWORD") == "1" {
		a.s.Config.Network.NoPassword = true
	}
	mobile := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = "192.168.50.20:1234"
		a.handler().ServeHTTP(w, r)
	}))
	defer mobile.Close()
	fmt.Printf("DLNA_QA_DESKTOP=%s\nDLNA_QA_MOBILE=%s\n", server.URL, mobile.URL)
	<-time.After(15 * time.Minute)
}
