package service

import (
	"bytes"
	"context"
	"image/jpeg"
	"os"
	"os/exec"
	"testing"
)

// ftypBox builds a minimal ISO-BMFF header: a 4-byte big-endian box size,
// the "ftyp" box type, and a 4-char major brand.
func ftypBox(brand string) []byte {
	b := []byte{0, 0, 0, 0x18} // box size (24); value is not validated by the sniffer
	b = append(b, []byte("ftyp")...)
	b = append(b, []byte(brand)...)
	b = append(b, make([]byte, 12)...) // minor version + compatible brands padding
	return b
}

func TestDetectReceiptFormat(t *testing.T) {
	pngMagic := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	jpegMagic := []byte{0xff, 0xd8, 0xff, 0xe0, 0, 0, 0, 0, 0, 0, 0, 0}

	tests := []struct {
		name     string
		data     []byte
		declared string
		want     string
	}{
		{"pdf magic", []byte("%PDF-1.7\n%âãÏÓ"), "application/octet-stream", "application/pdf"},
		{"pdf magic beats declared", []byte("%PDF-1.4"), "image/jpeg", "application/pdf"},
		{"heic brand", ftypBox("heic"), "", "image/heic"},
		{"heif mif1 brand", ftypBox("mif1"), "application/octet-stream", "image/heic"},
		{"heix brand", ftypBox("heix"), "image/heic", "image/heic"},
		{"avif is not heic", ftypBox("avif"), "", "application/octet-stream"},
		{"declared mime trusted for plain image", jpegMagic, "image/webp", "image/webp"},
		{"sniff png when declared octet-stream", pngMagic, "application/octet-stream", "image/png"},
		{"sniff jpeg when no declared mime", jpegMagic, "", "image/jpeg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectReceiptFormat(tt.data, tt.declared); got != tt.want {
				t.Errorf("detectReceiptFormat(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestPDFPopplerIndicatesEncrypted(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"incorrect_password", "Command Line Error: Incorrect password\n: exit status 1", true},
		{"password_required", "Error: Password required", true},
		{"needs_password", "This document needs a password", true},
		{"cannot_decrypt", "Cannot decrypt PDF file", true},
		{"encryption_not_supported", "encryption not supported", true},
		{"generic_failure", "Syntax Warning: bad pdf file\n", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PDFPopplerIndicatesEncrypted(tt.in); got != tt.want {
				t.Fatalf("PDFPopplerIndicatesEncrypted(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestPrepareReceiptForOCR_HEICRender verifies that a real HEIC upload is
// normalized to a decodable JPEG render before OCR. Skips when heif-convert is
// not installed (e.g. local macOS dev); runs in CI where libheif-tools ships.
// An empty API key is fine here — we only assert the conversion, not the OCR.
func TestPrepareReceiptForOCR_HEICRender(t *testing.T) {
	if _, err := exec.LookPath("heif-convert"); err != nil {
		t.Skip("heif-convert not installed; skipping HEIC normalization test")
	}
	data, err := os.ReadFile("testdata/images/receipt5.HEIC")
	if err != nil {
		t.Skipf("HEIC fixture unavailable: %v", err)
	}

	svc := NewOpenAIService("")
	render, mime, _ := svc.PrepareReceiptForOCR(context.Background(), data, "image/heic")

	if mime != "image/jpeg" {
		t.Fatalf("render mime = %q, want image/jpeg", mime)
	}
	if _, err := jpeg.Decode(bytes.NewReader(render)); err != nil {
		t.Fatalf("render is not a valid JPEG: %v", err)
	}
}
