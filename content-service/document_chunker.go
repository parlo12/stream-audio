package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"rsc.io/pdf"
)

func ChunkDocument(bookID uint, filePath string) (int, error) {
	text, err := ExtractTextByType(filePath)
	if err != nil {
		return 0, err
	}

	runes := []rune(text)
	chunkSize := 1000
	total := len(runes)
	count := 0

	for i := 0; i < total; i += chunkSize {
		end := i + chunkSize
		if end > total {
			end = total
		}
		chunk := BookChunk{
			BookID:    bookID,
			Index:     count,
			Content:   string(runes[i:end]),
			AudioPath: "",
		}
		db.Create(&chunk)
		count++
	}

	return count, nil
}

func ExtractTextByType(path string) (string, error) {
	lowerPath := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lowerPath, ".pdf"):
		return ExtractTextFromPDF(path)
	case strings.HasSuffix(lowerPath, ".txt"):
		return ExtractTextFromTXT(path)
	case strings.HasSuffix(lowerPath, ".epub"):
		return ExtractTextFromEPUB(path)
	case strings.HasSuffix(lowerPath, ".azw") || strings.HasSuffix(lowerPath, ".mobi") || strings.HasSuffix(lowerPath, ".azw3"):
		return ExtractTextFromMOBI(path)
	case strings.HasSuffix(lowerPath, ".kfx"):
		return "", errors.New("KFX format is not supported. Please convert to EPUB, PDF, MOBI, or AZW3 format first")
	default:
		return "", errors.New("unsupported file type. Supported formats: PDF, TXT, EPUB, MOBI, AZW, AZW3")
	}
}

// Add ExtractTextFromPDF, ExtractTextFromTXT, ExtractTextFromEPUB...
// You may already have this in utils — import and call it
func cleanUTF8(input []byte) string {
	// Your existing clean function goes here
	return string(input) // Replace this with your actual implementation
}

func ExtractTextFromTXT(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return cleanUTF8(data), nil
}

func ExtractTextFromPDF(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	stat, _ := file.Stat()
	reader, err := pdf.NewReader(file, stat.Size())
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	for i := 1; i <= reader.NumPage(); i++ {
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}
		content := page.Content()
		for _, text := range content.Text {
			buf.WriteString(text.S)
			buf.WriteString(" ")
		}
	}

	return buf.String(), nil
}

func ExtractTextFromEPUB(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer r.Close()

	var sb strings.Builder

	for _, f := range r.File {
		if strings.HasSuffix(strings.ToLower(f.Name), ".xhtml") || strings.HasSuffix(strings.ToLower(f.Name), ".html") {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			content, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				continue
			}
			sb.WriteString(string(content))
			sb.WriteString("\n")
		}
	}

	return sb.String(), nil
}

// ExtractTextFromMOBI extracts text from MOBI, AZW, and AZW3 files
// This function uses Calibre's ebook-convert command-line tool
func ExtractTextFromMOBI(path string) (string, error) {
	// Check if ebook-convert is available
	_, err := exec.LookPath("ebook-convert")
	if err != nil {
		return "", fmt.Errorf("ebook-convert (Calibre) not found. Please install Calibre to support MOBI/AZW formats. Error: %w", err)
	}

	// Create a temporary file for the converted text
	tempDir := os.TempDir()
	tempTxtFile := filepath.Join(tempDir, fmt.Sprintf("mobi_temp_%s.txt", filepath.Base(path)))
	defer os.Remove(tempTxtFile) // Clean up temp file

	// Run ebook-convert to convert MOBI to TXT
	cmd := exec.Command("ebook-convert", path, tempTxtFile, "--txt-output-encoding=utf-8")

	// Capture any errors from the conversion
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		return "", fmt.Errorf("failed to convert MOBI file: %w. Details: %s", err, stderr.String())
	}

	// Read the converted text file
	textData, err := os.ReadFile(tempTxtFile)
	if err != nil {
		return "", fmt.Errorf("failed to read converted text file: %w", err)
	}

	text := string(textData)
	if len(text) == 0 {
		return "", errors.New("no text content extracted from MOBI file")
	}

	return text, nil
}
