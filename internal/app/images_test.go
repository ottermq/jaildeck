package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

type apiImage struct {
	Name    string   `json:"name"`
	Source  string   `json:"source"`
	Ready   bool     `json:"ready"`
	State   string   `json:"state"`
	SHA512  string   `json:"sha512"`
	Guests  []string `json:"guests"`
	Running *struct {
		Step string `json:"step"`
	} `json:"running"`
}

func getImage(t *testing.T, router http.Handler, cookies []*http.Cookie, name string) (apiImage, int) {
	t.Helper()
	rec := do(t, router, http.MethodGet, "/api/vm-images/"+name, cookies, nil)
	var image apiImage
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &image); err != nil {
			t.Fatalf("decode: %v\n%s", err, rec.Body)
		}
	}
	return image, rec.Code
}

// awaitImage polls an image until nothing is running for it, as a client does
// after a 202 (JD-008).
func awaitImage(t *testing.T, router http.Handler, cookies []*http.Cookie, name string) apiImage {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		image, code := getImage(t, router, cookies, name)
		if code == http.StatusOK && image.Running == nil {
			return image
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never settled: %d %+v", name, code, image)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestVMImagesOverTheFakeAdapter(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	rec := do(t, router, http.MethodGet, "/api/vm-images", cookies, nil)
	var listing struct {
		VMBhyve string     `json:"vm_bhyve"`
		Images  []apiImage `json:"images"`
		Sources []struct {
			Name    string `json:"name"`
			Fetched bool   `json:"fetched"`
		} `json:"sources"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &listing) != nil {
		t.Fatalf("GET /api/vm-images: %d %s", rec.Code, rec.Body)
	}
	if listing.VMBhyve != "ready" || len(listing.Images) != 1 || !listing.Images[0].Ready ||
		len(listing.Sources) == 0 || !listing.Sources[0].Fetched {
		t.Fatalf("listing = %+v", listing)
	}

	// A second image from the same source: no body needed beyond the source,
	// 202, and ready once the client has watched it.
	rec = send(t, router, http.MethodPut, "/api/vm-images/trixie", cookies, `{"source":"debian-13"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("PUT returned %d, want 202: %s", rec.Code, rec.Body)
	}
	if image := awaitImage(t, router, cookies, "trixie"); !image.Ready || image.Source != "debian-13" {
		t.Errorf("trixie = %+v, want ready from debian-13", image)
	}
	if rec := send(t, router, http.MethodPut, "/api/vm-images/trixie", cookies, `{"source":"debian-13"}`); rec.Code != http.StatusOK {
		t.Errorf("repeating the PUT returned %d, want 200: %s", rec.Code, rec.Body)
	}

	// Removal: confirm, and never while a guest's disk is cloned from it.
	if rec := do(t, router, http.MethodDelete, "/api/vm-images/trixie", cookies, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("DELETE without confirm returned %d, want 400", rec.Code)
	}
	if rec := do(t, router, http.MethodDelete, "/api/vm-images/debian-13?confirm=debian-13", cookies, nil); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "jd-cap-running") {
		t.Errorf("DELETE of the image a guest uses returned %d: %s", rec.Code, rec.Body)
	}
	if rec := do(t, router, http.MethodDelete, "/api/vm-images/trixie?confirm=trixie", cookies, nil); rec.Code != http.StatusOK {
		t.Errorf("DELETE returned %d, want 200: %s", rec.Code, rec.Body)
	}
	if _, code := getImage(t, router, cookies, "trixie"); code != http.StatusNotFound {
		t.Errorf("GET after DELETE returned %d, want 404", code)
	}

	// The audit log has every stage, under its own name.
	rec = do(t, router, http.MethodGet, "/api/audit?limit=20", cookies, nil)
	for _, operation := range []string{"write-image", "seal-image", "destroy-image"} {
		if !strings.Contains(rec.Body.String(), `"`+operation+`"`) {
			t.Errorf("the audit log has no %s: %s", operation, rec.Body)
		}
	}
}

func TestAVMImageWhoseWriteFailsIsWrittenAndNotReady(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	if rec := send(t, router, http.MethodPut, "/api/vm-images/broken", cookies, `{"source":"fails-to-write"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("PUT returned %d: %s", rec.Code, rec.Body)
	}
	if image := awaitImage(t, router, cookies, "broken"); image.Ready || image.State != "written" {
		t.Errorf("broken = %+v, want written and not ready", image)
	}
	if rec := send(t, router, http.MethodPut, "/api/vm-images/broken", cookies, `{"source":"fails-to-write"}`); rec.Code != http.StatusConflict {
		t.Errorf("preparing it again returned %d, want 409: %s", rec.Code, rec.Body)
	}
	if rec := send(t, router, http.MethodPut, "/api/vm-images/other", cookies, `{"source":"nonesuch"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown source returned %d, want 400", rec.Code)
	}
}
