package webserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"

	internalservice "github.com/Vero-Receipts/vero-mcp/internal/service"
	pkgservice "github.com/Vero-Receipts/vero-mcp/pkg/service"
)

func mimeFromFilename(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".heic":
		return "image/heic"
	default:
		return "image/jpeg"
	}
}

func extFromFilename(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" {
		return ".jpg"
	}
	return ext
}

// UploadResult holds the result of a receipt upload.
type UploadResult struct {
	Data  json.RawMessage
	Error string
}

// LocalWebServer is an ephemeral HTTP server for Plaid Link and receipt upload flows.
type LocalWebServer struct {
	plaidSvc   *pkgservice.PlaidService
	receiptSvc *pkgservice.ReceiptService
	storageSvc *internalservice.StorageService
	userID     uuid.UUID

	mu       sync.Mutex
	listener net.Listener
	srv      *http.Server
	baseURL  string
	running  bool

	// Plaid Link flow
	linkMu       sync.Mutex
	pendingLinks map[string]chan string

	// Receipt upload flow
	uploadMu       sync.Mutex
	pendingUploads map[string]chan *UploadResult
}

// NewLocalWebServer creates a new LocalWebServer instance.
func NewLocalWebServer(
	plaidSvc *pkgservice.PlaidService,
	receiptSvc *pkgservice.ReceiptService,
	storageSvc *internalservice.StorageService,
	userID uuid.UUID,
) *LocalWebServer {
	return &LocalWebServer{
		plaidSvc:       plaidSvc,
		receiptSvc:     receiptSvc,
		storageSvc:     storageSvc,
		userID:         userID,
		pendingLinks:   make(map[string]chan string),
		pendingUploads: make(map[string]chan *UploadResult),
	}
}

// EnsureRunning starts the web server if it's not already running.
// Returns the base URL (e.g., "http://localhost:54321").
func (w *LocalWebServer) EnsureRunning() (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.running {
		return w.baseURL, nil
	}

	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return "", fmt.Errorf("listen: %w", err)
	}

	w.listener = listener
	port := listener.Addr().(*net.TCPAddr).Port
	w.baseURL = fmt.Sprintf("http://localhost:%d", port)

	// Update storage service base URL so receipt image URLs are correct.
	w.storageSvc.SetBaseURL(w.baseURL)

	mux := http.NewServeMux()

	// Plaid Link pages
	linkTmpl := template.Must(template.New("plaid-link").Parse(plaidLinkHTML))
	mux.HandleFunc("/plaid/link", w.handlePlaidLink(linkTmpl))
	mux.HandleFunc("/plaid/callback", w.handlePlaidCallback())
	mux.HandleFunc("/plaid/success", w.handlePlaidSuccess())

	// Receipt upload pages
	uploadTmpl := template.Must(template.New("receipt-upload").Parse(receiptUploadHTML))
	mux.HandleFunc("/receipts/upload", w.handleReceiptUploadPage(uploadTmpl))
	mux.HandleFunc("/api/receipts/upload", w.handleReceiptUpload())

	// Static file server for receipt images
	mux.Handle("/files/", http.StripPrefix("/files/", http.FileServer(http.Dir(w.storageSvc.BaseDir()))))

	w.srv = &http.Server{Handler: mux}
	w.running = true

	go func() {
		slog.Info("local web server started", "url", w.baseURL)
		if err := w.srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Error("local web server error", "error", err)
		}
	}()

	return w.baseURL, nil
}

// StorageSvc returns the underlying storage service.
func (w *LocalWebServer) StorageSvc() *internalservice.StorageService {
	return w.storageSvc
}

// Stop shuts down the web server.
func (w *LocalWebServer) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.running && w.srv != nil {
		w.srv.Close()
		w.running = false
		slog.Info("local web server stopped")
	}
}

// RegisterLinkWait creates a channel to wait for a Plaid Link completion.
func (w *LocalWebServer) RegisterLinkWait(linkToken string) <-chan string {
	w.linkMu.Lock()
	defer w.linkMu.Unlock()

	ch := make(chan string, 1)
	w.pendingLinks[linkToken] = ch
	return ch
}

// CleanupLinkWait removes a pending link token.
func (w *LocalWebServer) CleanupLinkWait(linkToken string) {
	w.linkMu.Lock()
	defer w.linkMu.Unlock()
	delete(w.pendingLinks, linkToken)
}

// RegisterUploadSession creates a channel to wait for a receipt upload.
func (w *LocalWebServer) RegisterUploadSession(sessionID string) <-chan *UploadResult {
	w.uploadMu.Lock()
	defer w.uploadMu.Unlock()

	ch := make(chan *UploadResult, 1)
	w.pendingUploads[sessionID] = ch
	return ch
}

// GetUploadChannel returns the upload channel for a session, if it exists.
func (w *LocalWebServer) GetUploadChannel(sessionID string) (<-chan *UploadResult, bool) {
	w.uploadMu.Lock()
	defer w.uploadMu.Unlock()
	ch, ok := w.pendingUploads[sessionID]
	return ch, ok
}

// CleanupUploadSession removes a pending upload session.
func (w *LocalWebServer) CleanupUploadSession(sessionID string) {
	w.uploadMu.Lock()
	defer w.uploadMu.Unlock()
	delete(w.pendingUploads, sessionID)
}

// --- HTTP Handlers ---

func (w *LocalWebServer) handlePlaidLink(tmpl *template.Template) http.HandlerFunc {
	return func(wr http.ResponseWriter, r *http.Request) {
		linkToken := r.URL.Query().Get("token")
		if linkToken == "" {
			http.Error(wr, "Missing token parameter", http.StatusBadRequest)
			return
		}
		wr.Header().Set("Content-Type", "text/html")
		tmpl.Execute(wr, map[string]string{
			"LinkToken":   linkToken,
			"CallbackURL": "/plaid/callback",
		})
	}
}

func (w *LocalWebServer) handlePlaidCallback() http.HandlerFunc {
	return func(wr http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			PublicToken string `json:"public_token"`
			LinkToken   string `json:"link_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(wr, "Invalid request", http.StatusBadRequest)
			return
		}

		if req.PublicToken == "" || req.LinkToken == "" {
			http.Error(wr, "Missing public_token or link_token", http.StatusBadRequest)
			return
		}

		w.linkMu.Lock()
		ch, ok := w.pendingLinks[req.LinkToken]
		w.linkMu.Unlock()

		if ok {
			ch <- req.PublicToken
		}

		wr.Header().Set("Content-Type", "application/json")
		json.NewEncoder(wr).Encode(map[string]bool{"success": true})
	}
}

func (w *LocalWebServer) handlePlaidSuccess() http.HandlerFunc {
	return func(wr http.ResponseWriter, r *http.Request) {
		wr.Header().Set("Content-Type", "text/html")
		fmt.Fprint(wr, successHTML)
	}
}

func (w *LocalWebServer) handleReceiptUploadPage(tmpl *template.Template) http.HandlerFunc {
	return func(wr http.ResponseWriter, r *http.Request) {
		sessionID := r.URL.Query().Get("session")
		if sessionID == "" {
			http.Error(wr, "Missing session parameter", http.StatusBadRequest)
			return
		}

		// Verify the session exists.
		if _, ok := w.GetUploadChannel(sessionID); !ok {
			http.Error(wr, "Invalid or expired session", http.StatusBadRequest)
			return
		}

		wr.Header().Set("Content-Type", "text/html")
		tmpl.Execute(wr, map[string]string{
			"SessionID":   sessionID,
			"CallbackURL": "/api/receipts/upload",
		})
	}
}

func (w *LocalWebServer) handleReceiptUpload() http.HandlerFunc {
	return func(wr http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		sessionID := r.FormValue("session_id")
		if sessionID == "" {
			http.Error(wr, "Missing session_id", http.StatusBadRequest)
			return
		}

		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(wr, "Invalid multipart form", http.StatusBadRequest)
			return
		}

		file, header, err := r.FormFile("receipt_image")
		if err != nil {
			http.Error(wr, "Missing receipt_image field", http.StatusBadRequest)
			return
		}
		defer file.Close()

		imageData, err := io.ReadAll(file)
		if err != nil {
			http.Error(wr, "Failed to read image", http.StatusInternalServerError)
			return
		}

		// Sync transactions first to have latest data for matching.
		w.plaidSvc.SyncTransactions(r.Context(), w.userID, "")

		// Determine MIME type from filename.
		mimeType := mimeFromFilename(header.Filename)

		// Save image to local storage (private; host resolves to presigned URL at read time).
		imageKey := uuid.New().String() + extFromFilename(header.Filename)
		imageURL := ""
		if w.storageSvc != nil {
			if err2 := w.storageSvc.UploadPrivate(r.Context(), imageKey, bytes.NewReader(imageData), mimeType); err2 != nil {
				slog.Warn("failed to save receipt image locally", "error", err2)
			} else {
				imageURL = imageKey
			}
		}

		// Process the receipt: OCR + auto-match.
		result, err := w.receiptSvc.Scan(r.Context(), w.userID, imageData, mimeType, imageURL, nil, nil)

		// Notify waiting MCP tool.
		w.uploadMu.Lock()
		ch, ok := w.pendingUploads[sessionID]
		w.uploadMu.Unlock()

		if ok {
			if err != nil {
				ch <- &UploadResult{Error: err.Error()}
			} else {
				resultJSON, _ := json.Marshal(result)
				ch <- &UploadResult{Data: resultJSON}
			}
		}

		wr.Header().Set("Content-Type", "application/json")
		if err != nil {
			slog.Error("receipt upload failed", "error", err)
			wr.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(wr).Encode(map[string]interface{}{"success": false, "error": err.Error()})
			return
		}

		json.NewEncoder(wr).Encode(map[string]interface{}{"success": true})
	}
}
