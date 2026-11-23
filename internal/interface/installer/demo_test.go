package installer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// The question whether the new database is to hold demo data travels with the
// connection it is asked of, in the language the page was read in, and nothing
// asks for demo data on its own.
//
// The installer only carries the question: the application answers it once it
// has migrated the database, because only then is there anything to fill. So
// what has to hold here is that the save hands it over intact, and that a save
// which did not choose it - every installation that will be worked in - is
// handed over as not asking.
func TestASaveHandsOverWhetherDemoDataWasAskedFor(t *testing.T) {
	for _, c := range []struct {
		name     string
		extra    string
		demo     bool
		language string
	}{
		{name: "asked, in German", extra: `,"demo":true,"language":"de"`, demo: true, language: "de"},
		{name: "not asked", extra: ``, demo: false, language: ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, rec := saveTo(t, c.extra)

			if rec.Code != http.StatusOK {
				t.Fatalf("the answer was refused with %d: %s", rec.Code, rec.Body)
			}

			handed := <-s.done

			if handed.demo != c.demo || handed.language != c.language {
				t.Errorf("handed over demo %v in %q, want %v in %q",
					handed.demo, handed.language, c.demo, c.language)
			}
		})
	}
}

// A new database is filled with demo data or from a backup, never both: the
// demo goes only into an empty database, which a restored one is not, so a
// save asking for both is refused on this screen rather than half answered at
// the start.
func TestASaveAskingForTheDemoAndABackupIsRefused(t *testing.T) {
	s, rec := saveTo(t, `,"demo":true,"language":"de","restore":true`)

	var body struct {
		Code string `json:"code"`
	}

	_ = json.Unmarshal(rec.Body.Bytes(), &body)

	if rec.Code == http.StatusOK || body.Code != "demoOrRestore" {
		t.Errorf("a save asking for the demo and a backup was answered %d: %s", rec.Code, rec.Body)
	}

	select {
	case handed := <-s.done:
		t.Errorf("the refused save handed over %+v", handed)
	default:
	}
}

// saveTo answers the installer with a working SQLite connection and what extra
// adds to it, and returns the server and its answer.
func saveTo(t *testing.T, extra string) (*server, *httptest.ResponseRecorder) {
	t.Helper()

	dir := t.TempDir()

	s := &server{
		cfg:  Config{Token: "the-token", DatasourceFile: filepath.Join(dir, "datasource.json"), Logf: t.Logf},
		done: make(chan handover, 1),
	}

	body := fmt.Sprintf(`{"dialect":"sqlite","name":%q%s}`, filepath.ToSlash(filepath.Join(dir, "qubitdesk")), extra)
	req := httptest.NewRequest(http.MethodPost, "/install/save", strings.NewReader(body))
	req.Header.Set("X-Setup-Token", "the-token")

	rec := httptest.NewRecorder()
	s.save(rec, req)

	return s, rec
}
