package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go-server/models"
	"go-server/utils"
)

func appearanceReq(t *testing.T, method, userID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/appearance", strings.NewReader(body))
	if userID != "" {
		token, _ := utils.GenerateToken("u", userID)
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	AppearanceHandler(rec, req)
	return rec
}

func newAppearanceUser() string {
	return "appearance-user-" + time.Now().Format("20060102150405.000000000")
}

func TestAppearanceGetBeforeSaveReturns404(t *testing.T) {
	rec := appearanceReq(t, "GET", newAppearanceUser(), "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAppearancePutThenGet(t *testing.T) {
	uid := newAppearanceUser()
	doc := `{"head":"#8fd14f","body":"#d400c8","eyes":"#e8dcc0"}`
	assert.Equal(t, http.StatusOK, appearanceReq(t, "PUT", uid, doc).Code)

	rec := appearanceReq(t, "GET", uid, "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, doc, rec.Body.String())

	// PUT replaces the whole document
	doc2 := `{"head":"#000000","body":"#ffffff","eyes":"#123456"}`
	assert.Equal(t, http.StatusOK, appearanceReq(t, "PUT", uid, doc2).Code)
	assert.JSONEq(t, doc2, appearanceReq(t, "GET", uid, "").Body.String())
}

func TestAppearancePutInvalidReturns400AndSavesNothing(t *testing.T) {
	bad := []string{
		`{"head":"red"}`,
		`{"head":"#12345"}`,
		`{"head":"#gggggg"}`,
		`{"head":"#8fd14f","skin":"#8fd14f"}`,
		`{"head":5}`,
		`not json`,
		`null`,
	}
	for _, body := range bad {
		uid := newAppearanceUser()
		assert.Equal(t, http.StatusBadRequest, appearanceReq(t, "PUT", uid, body).Code, body)
		assert.Equal(t, http.StatusNotFound, appearanceReq(t, "GET", uid, "").Code, body)
	}
}

func TestAppearanceInvalidPutKeepsExistingDocument(t *testing.T) {
	uid := newAppearanceUser()
	doc := `{"head":"#8fd14f"}`
	appearanceReq(t, "PUT", uid, doc)
	assert.Equal(t, http.StatusBadRequest, appearanceReq(t, "PUT", uid, `{"head":"nope"}`).Code)
	assert.JSONEq(t, doc, appearanceReq(t, "GET", uid, "").Body.String())
}

func TestAppearanceRequiresToken(t *testing.T) {
	for _, m := range []string{"GET", "PUT"} {
		assert.Equal(t, http.StatusUnauthorized, appearanceReq(t, m, "", `{"head":"#8fd14f"}`).Code)
	}
}

func TestInitDBAddsAppearancesTableToExistingDatabase(t *testing.T) {
	_, err := models.DB.Exec("DROP TABLE IF EXISTS appearances")
	assert.NoError(t, err)
	assert.NoError(t, models.InitDB())
	assert.NoError(t, models.SaveAppearance("migration-check", []byte(`{}`)))
}
