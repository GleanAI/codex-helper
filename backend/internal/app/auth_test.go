package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"codex-helper/internal/security"
)

func newAuthenticatedTestApp(t *testing.T) *App {
	t.Helper()
	a := newReminderTestApp(t)
	if _, err := a.store.DB.Exec(
		"INSERT INTO admin(id,username,password_hash,created_at) VALUES(1,?,?,?)",
		"admin", security.Password("current-password"), time.Now().Unix(),
	); err != nil {
		t.Fatal(err)
	}
	if err := a.store.Set("initialized", "true"); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"current-session", "other-session"} {
		if _, err := a.store.DB.Exec(
			"INSERT INTO sessions(token_hash,expires_at,created_at) VALUES(?,?,?)",
			security.HashToken(token), time.Now().Add(time.Hour).Unix(), time.Now().Unix(),
		); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

func credentialRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPut, "/api/v1/auth/credentials", bytes.NewBufferString(body))
	request.AddCookie(&http.Cookie{Name: "session", Value: "current-session"})
	request.Header.Set("X-Requested-With", "codex-helper")
	return request
}

func TestUpdateCredentialsChangesLoginAndRevokesOtherSessions(t *testing.T) {
	a := newAuthenticatedTestApp(t)
	recorder := httptest.NewRecorder()
	a.api(recorder, credentialRequest(`{"username":"renamed-admin","currentPassword":"current-password","newPassword":"replacement-password"}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Username != "renamed-admin" {
		t.Fatalf("username = %q", response.Username)
	}

	var username, hash string
	if err := a.store.DB.QueryRow("SELECT username,password_hash FROM admin WHERE id=1").Scan(&username, &hash); err != nil {
		t.Fatal(err)
	}
	if username != "renamed-admin" || !security.VerifyPassword(hash, "replacement-password") || security.VerifyPassword(hash, "current-password") {
		t.Fatalf("stored credentials were not replaced")
	}
	if !a.authed(credentialRequest(`{}`)) {
		t.Fatal("current session was revoked")
	}
	other := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	other.AddCookie(&http.Cookie{Name: "session", Value: "other-session"})
	if a.authed(other) {
		t.Fatal("other session remains valid")
	}

	oldLogin := httptest.NewRecorder()
	a.api(oldLogin, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"current-password"}`)))
	if oldLogin.Code != http.StatusUnauthorized {
		t.Fatalf("old login status = %d, body = %s", oldLogin.Code, oldLogin.Body.String())
	}
	newLogin := httptest.NewRecorder()
	a.api(newLogin, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"renamed-admin","password":"replacement-password"}`)))
	if newLogin.Code != http.StatusOK {
		t.Fatalf("new login status = %d, body = %s", newLogin.Code, newLogin.Body.String())
	}
}

func TestUpdateCredentialsSupportsIndependentAndNoopChanges(t *testing.T) {
	t.Run("username only", func(t *testing.T) {
		a := newAuthenticatedTestApp(t)
		recorder := httptest.NewRecorder()
		a.api(recorder, credentialRequest(`{"username":"renamed-admin","currentPassword":"current-password","newPassword":""}`))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		var hash string
		if err := a.store.DB.QueryRow("SELECT password_hash FROM admin WHERE id=1").Scan(&hash); err != nil {
			t.Fatal(err)
		}
		if !security.VerifyPassword(hash, "current-password") {
			t.Fatal("username-only update changed the password")
		}
	})

	t.Run("password only", func(t *testing.T) {
		a := newAuthenticatedTestApp(t)
		recorder := httptest.NewRecorder()
		a.api(recorder, credentialRequest(`{"username":"admin","currentPassword":"current-password","newPassword":"replacement-password"}`))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		var username string
		if err := a.store.DB.QueryRow("SELECT username FROM admin WHERE id=1").Scan(&username); err != nil {
			t.Fatal(err)
		}
		if username != "admin" {
			t.Fatalf("username = %q", username)
		}
	})

	t.Run("no changes", func(t *testing.T) {
		a := newAuthenticatedTestApp(t)
		recorder := httptest.NewRecorder()
		a.api(recorder, credentialRequest(`{"username":"admin","currentPassword":"current-password","newPassword":""}`))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		var count int
		if err := a.store.DB.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 2 {
			t.Fatalf("session count = %d; want 2", count)
		}
	})
}

func TestUpdateCredentialsRejectsInvalidRequestsWithoutChanges(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		status int
	}{
		{"wrong current password", `{"username":"renamed-admin","currentPassword":"wrong","newPassword":""}`, http.StatusForbidden},
		{"short username", `{"username":"ab","currentPassword":"current-password","newPassword":""}`, http.StatusBadRequest},
		{"short new password", `{"username":"admin","currentPassword":"current-password","newPassword":"short"}`, http.StatusBadRequest},
		{"unknown field", `{"username":"admin","currentPassword":"current-password","newPassword":"","password":"leak"}`, http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			a := newAuthenticatedTestApp(t)
			recorder := httptest.NewRecorder()
			a.api(recorder, credentialRequest(test.body))
			if recorder.Code != test.status {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			var username, hash string
			if err := a.store.DB.QueryRow("SELECT username,password_hash FROM admin WHERE id=1").Scan(&username, &hash); err != nil {
				t.Fatal(err)
			}
			if username != "admin" || !security.VerifyPassword(hash, "current-password") {
				t.Fatal("invalid request changed credentials")
			}
			var count int
			if err := a.store.DB.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 2 {
				t.Fatalf("session count = %d; want 2", count)
			}
		})
	}
}

func TestUpdateCredentialsRequiresSessionAndRequestSource(t *testing.T) {
	a := newAuthenticatedTestApp(t)
	body := `{"username":"renamed-admin","currentPassword":"current-password","newPassword":""}`

	unauthenticated := httptest.NewRecorder()
	a.api(unauthenticated, httptest.NewRequest(http.MethodPut, "/api/v1/auth/credentials", bytes.NewBufferString(body)))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthenticated.Code)
	}

	missingSourceRequest := httptest.NewRequest(http.MethodPut, "/api/v1/auth/credentials", bytes.NewBufferString(body))
	missingSourceRequest.AddCookie(&http.Cookie{Name: "session", Value: "current-session"})
	missingSource := httptest.NewRecorder()
	a.api(missingSource, missingSourceRequest)
	if missingSource.Code != http.StatusForbidden {
		t.Fatalf("missing source status = %d", missingSource.Code)
	}
}

func TestReplaceCredentialsRejectsStalePasswordHash(t *testing.T) {
	a := newAuthenticatedTestApp(t)
	var staleHash string
	if err := a.store.DB.QueryRow("SELECT password_hash FROM admin WHERE id=1").Scan(&staleHash); err != nil {
		t.Fatal(err)
	}
	winningHash := security.Password("winning-password")
	if _, err := a.store.DB.Exec(
		"UPDATE admin SET username=?,password_hash=? WHERE id=1",
		"winning-admin", winningHash,
	); err != nil {
		t.Fatal(err)
	}

	updated, err := a.replaceCredentials(
		"stale-admin",
		security.Password("stale-password"),
		staleHash,
		"current-session",
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("stale credentials unexpectedly replaced the winning update")
	}
	var username, storedHash string
	if err = a.store.DB.QueryRow("SELECT username,password_hash FROM admin WHERE id=1").Scan(&username, &storedHash); err != nil {
		t.Fatal(err)
	}
	if username != "winning-admin" || storedHash != winningHash {
		t.Fatalf("stored credentials = %q, %q", username, storedHash)
	}
	var sessions int
	if err = a.store.DB.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 2 {
		t.Fatalf("session count = %d; want 2", sessions)
	}
}
