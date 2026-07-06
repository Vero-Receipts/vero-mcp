package service

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
)

// OCRErrPDFEncrypted is returned in OCRResult.Error when Poppler reports an
// encrypted PDF we intentionally skip (no OpenAI, no image fallback).
const OCRErrPDFEncrypted = "pdf_encrypted"

// PrepareReceiptForOCR normalizes arbitrary receipt bytes into a vision-friendly
// render, runs OCR, and returns BOTH the structured OCR result and a displayable
// render (JPEG/PNG) suitable for thumbnailing and storage.
//
//   - HEIC  -> heif-convert to JPEG, then vision OCR on the JPEG
//   - PDF   -> pdftotext (text path) or pdftoppm->PNG (vision fallback);
//     render is always the rasterized first page when available
//   - other -> vision OCR unchanged; render == the original bytes
//
// Callers should persist renderBytes/renderMime as the displayable image so that
// HEIC and PDF receipts get a browser-renderable URL and a real thumbnail.
//
// Format is detected from the byte content (magic bytes), not the caller-supplied
// mimeType, because upload Content-Type headers and file extensions are
// unreliable. mimeType is used only as a hint for non-HEIC/PDF images.
func (s *OpenAIService) PrepareReceiptForOCR(ctx context.Context, data []byte, mimeType string) (renderBytes []byte, renderMime string, ocr *domain.OCRResult) {
	switch detectReceiptFormat(data, mimeType) {
	case "application/pdf":
		return s.preparePDFForOCR(ctx, data)
	case "image/heic":
		jpegBytes, err := heicToJPEG(ctx, data)
		if err != nil {
			slog.Warn("[OCR] heic->jpeg conversion failed", "error", err)
			return data, mimeType, &domain.OCRResult{Error: fmt.Sprintf("heic conversion failed: %v", err)}
		}
		return jpegBytes, "image/jpeg", s.ParseImageData(ctx, jpegBytes, "image/jpeg")
	default:
		return data, mimeType, s.ParseImageData(ctx, data, mimeType)
	}
}

// detectReceiptFormat sniffs the byte content to classify a receipt upload.
// Returns "application/pdf", "image/heic", or the best-effort image mime
// (declared hint, else http.DetectContentType).
func detectReceiptFormat(data []byte, declaredMime string) string {
	if bytes.HasPrefix(data, []byte("%PDF-")) {
		return "application/pdf"
	}
	// HEIC/HEIF is ISO-BMFF: bytes[4:8] == "ftyp", brand at bytes[8:12].
	// http.DetectContentType does NOT recognize HEIC (returns octet-stream),
	// so we check the brand explicitly.
	if len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")) {
		switch string(data[8:12]) {
		case "heic", "heix", "heim", "heis", "hevc", "hevx", "mif1", "msf1":
			return "image/heic"
		}
	}
	if declaredMime != "" && declaredMime != "application/octet-stream" {
		return declaredMime
	}
	return http.DetectContentType(data)
}

// preparePDFForOCR rasterizes the first PDF page for a displayable render, then
// runs OCR via text extraction (preferred) or vision on the rasterized page.
func (s *OpenAIService) preparePDFForOCR(ctx context.Context, data []byte) ([]byte, string, *domain.OCRResult) {
	tmp, err := os.CreateTemp("", "receipt-*.pdf")
	if err != nil {
		return data, "application/pdf", &domain.OCRResult{Error: fmt.Sprintf("temp pdf: %v", err)}
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return data, "application/pdf", &domain.OCRResult{Error: fmt.Sprintf("write pdf: %v", err)}
	}
	tmp.Close()

	// Rasterize the first page so the UI always has something to display, even
	// when OCR succeeds via the (cheaper) text path. Falls back to the raw PDF
	// bytes if rasterization fails (e.g. poppler missing).
	render := data
	renderMime := "application/pdf"
	if imgPath, cerr := ConvertPDFToImage(ctx, tmp.Name()); cerr == nil {
		if b, rerr := os.ReadFile(imgPath); rerr == nil {
			render, renderMime = b, "image/png"
		}
		os.Remove(imgPath)
	}

	text, err := ExtractPDFText(ctx, tmp.Name())
	if err != nil {
		if PDFPopplerIndicatesEncrypted(err.Error()) {
			slog.Info("[OCR] skipping encrypted PDF")
			return render, renderMime, &domain.OCRResult{Error: OCRErrPDFEncrypted}
		}
		slog.Warn("[OCR] pdftotext failed, falling back to vision", "error", err)
	}
	if strings.TrimSpace(text) != "" {
		slog.Info("[OCR] parsing PDF via text extraction", "text_length", len(text))
		return render, renderMime, s.ParseTextAsReceipt(ctx, text, "", nil)
	}

	if renderMime == "image/png" {
		slog.Info("[OCR] PDF text empty, parsing rasterized page via vision")
		return render, renderMime, s.ParseImageData(ctx, render, "image/png")
	}
	return render, renderMime, &domain.OCRResult{Error: "pdf has no extractable text and rasterization failed"}
}

// heicToJPEG converts HEIC/HEIF bytes to JPEG by shelling out to heif-convert
// (libheif-tools), mirroring the poppler-based PDF helpers. OpenAI's vision API
// does not accept HEIC, so this normalization is required before OCR.
func heicToJPEG(ctx context.Context, data []byte) ([]byte, error) {
	in, err := os.CreateTemp("", "receipt-*.heic")
	if err != nil {
		return nil, fmt.Errorf("temp heic: %w", err)
	}
	defer os.Remove(in.Name())
	if _, err := in.Write(data); err != nil {
		in.Close()
		return nil, fmt.Errorf("write heic: %w", err)
	}
	in.Close()

	if err := validateCommandPath(in.Name()); err != nil {
		return nil, err
	}
	outPath := in.Name() + ".jpg"
	// #nosec G204 -- in.Name() is validated by validateCommandPath above.
	cmd := exec.CommandContext(ctx, "heif-convert", "-q", "90", in.Name(), outPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("heif-convert: %s: %w", stderr.String(), err)
	}

	// heif-convert writes outPath for a single image, but some versions suffix
	// the primary image index (e.g. <name>.jpg-1.jpg); glob to be safe.
	out, rerr := os.ReadFile(outPath)
	if rerr != nil {
		matches, _ := filepath.Glob(in.Name() + "*.jpg")
		if len(matches) == 0 {
			return nil, fmt.Errorf("heif-convert produced no output")
		}
		outPath = matches[0]
		out, rerr = os.ReadFile(outPath)
		if rerr != nil {
			return nil, fmt.Errorf("read converted jpeg: %w", rerr)
		}
	}
	defer os.Remove(outPath)
	if len(out) == 0 {
		return nil, fmt.Errorf("heif-convert produced empty output")
	}
	return out, nil
}

// --- poppler (pdftotext / pdftoppm) helpers ---
// These are the single source of truth for the PDF OCR pipeline: the shared
// upload entrypoint (preparePDFForOCR) and the email-ingestion path both call
// the exported variants below, so a fix lands in one place. Requires
// poppler-utils on the runtime image.

func validateCommandPath(path string) error {
	clean := filepath.Clean(path)
	if strings.Contains(clean, "..") {
		return fmt.Errorf("invalid command path")
	}
	if !filepath.IsAbs(clean) {
		return fmt.Errorf("command path must be absolute")
	}
	return nil
}

// PDFPopplerIndicatesEncrypted reports whether a poppler stderr/error string
// means the PDF is password-protected (so callers can skip it cleanly).
func PDFPopplerIndicatesEncrypted(stderrOrErr string) bool {
	s := strings.ToLower(stderrOrErr)
	return strings.Contains(s, "incorrect password") ||
		strings.Contains(s, "password required") ||
		strings.Contains(s, "needs a password") ||
		strings.Contains(s, "cannot decrypt") ||
		strings.Contains(s, "encryption not supported")
}

// ExtractPDFText uses pdftotext (poppler-utils) to extract text from a PDF.
func ExtractPDFText(ctx context.Context, pdfPath string) (string, error) {
	if err := validateCommandPath(pdfPath); err != nil {
		return "", err
	}
	// #nosec G204 -- pdfPath is validated by validateCommandPath above.
	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", pdfPath, "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pdftotext: %s: %w", stderr.String(), err)
	}
	return stdout.String(), nil
}

// ConvertPDFToImage uses pdftoppm (poppler-utils) to render the first page as PNG
// and returns the path to the generated file (caller is responsible for removal).
func ConvertPDFToImage(ctx context.Context, pdfPath string) (string, error) {
	if err := validateCommandPath(pdfPath); err != nil {
		return "", err
	}
	outPrefix := pdfPath + "_page"
	// #nosec G204 -- pdfPath is validated by validateCommandPath above.
	cmd := exec.CommandContext(ctx, "pdftoppm",
		"-png", "-f", "1", "-l", "1", "-r", "300", pdfPath, outPrefix)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pdftoppm: %s: %w", stderr.String(), err)
	}
	matches, _ := filepath.Glob(outPrefix + "*.png")
	if len(matches) == 0 {
		return "", fmt.Errorf("pdftoppm produced no output files")
	}
	return matches[0], nil
}
