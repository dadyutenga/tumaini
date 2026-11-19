package org

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"bms/internal/platform/httpx"
)

// allowedTypes are the file types the API accepts. The type is decided from the
// file's own bytes, never from the client's Content-Type or file name, and HTML,
// SVG and executables are not on the list.
var allowedTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
	"application/pdf": true, "text/plain": true, "application/zip": true,
}

var imageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// readUpload reads at most max bytes and returns them with the detected type.
func readUpload(r io.Reader, max int64, imagesOnly bool) ([]byte, string, error) {
	buf, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(buf)) > max {
		return nil, "", httpx.New(http.StatusRequestEntityTooLarge, "file_too_large", "That file is too large")
	}
	if len(buf) == 0 {
		return nil, "", httpx.BadRequest("invalid_body", "The file is empty")
	}
	mime := detect(buf)
	if !allowedTypes[mime] || (imagesOnly && !imageTypes[mime]) {
		return nil, "", httpx.New(http.StatusUnsupportedMediaType, "unsupported_file", "That file type is not allowed. Use an image, PDF, text, CSV, or Excel/Word file.")
	}
	return buf, mime, nil
}

func detect(buf []byte) string {
	head := buf
	if len(head) > 512 {
		head = head[:512]
	}
	mime := strings.TrimSpace(strings.SplitN(http.DetectContentType(head), ";", 2)[0])
	// Markup is reported as text/html or text/xml, which are not allowed. SVG has no
	// signature and looks like plain text, so plain text that starts with a tag is refused too.
	if mime == "text/plain" && bytes.HasPrefix(bytes.TrimSpace(head), []byte("<")) {
		return "text/markup"
	}
	return mime
}

func reader(b []byte) io.Reader { return bytes.NewReader(b) }
