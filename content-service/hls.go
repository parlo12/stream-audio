package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// packageHLS segments a page's final audio into HLS (.ts segments + .m3u8) and
// uploads them to R2 under audio/{book}/{page}/hls/. Returns the playlist key.
func packageHLS(bookID uint, pageIndex int, finalAudio string) (string, error) {
	jobDir, err := os.MkdirTemp("", "hls-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(jobDir)

	src, cleanup, err := localizeMedia(context.Background(), finalAudio)
	if err != nil {
		return "", err
	}
	defer cleanup()

	playlist := filepath.Join(jobDir, "page.m3u8")
	cmd := exec.Command("ffmpeg", "-y", "-i", src,
		"-c:a", "aac", "-b:a", "128k",
		"-f", "hls", "-hls_time", "10", "-hls_playlist_type", "vod",
		"-hls_segment_filename", filepath.Join(jobDir, "seg_%03d.ts"),
		playlist)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("ffmpeg hls: %v\n%s", err, out)
	}

	prefix := fmt.Sprintf("audio/%d/%d/hls/", bookID, pageIndex)
	entries, _ := os.ReadDir(jobDir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ct := "audio/mp2t"
		if strings.HasSuffix(name, ".m3u8") {
			ct = "application/vnd.apple.mpegurl"
		}
		if err := store.PutFile(context.Background(), prefix+name, filepath.Join(jobDir, name), ct); err != nil {
			return "", fmt.Errorf("upload %s: %w", name, err)
		}
	}
	return prefix + "page.m3u8", nil
}

// serveHLSHandler (GET /user/books/:book_id/pages/:page/hls.m3u8) returns the
// page's HLS playlist with each segment line rewritten to a short-lived
// presigned R2 URL — AVPlayer fetches the .ts segments straight from R2.
func serveHLSHandler(c *gin.Context) {
	bookID, _ := strconv.Atoi(c.Param("book_id"))
	pageIndex, _ := strconv.Atoi(c.Param("page"))
	chunkIndex := pageIndex - 1

	var chunk BookChunk
	if err := db.Where("book_id = ? AND \"index\" = ?", bookID, chunkIndex).First(&chunk).Error; err != nil || chunk.HLSPath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "HLS not available for this page"})
		return
	}

	tmp, err := os.CreateTemp("", "pl-*.m3u8")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "tmp"})
		return
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if err := store.GetToFile(c.Request.Context(), chunk.HLSPath, tmp.Name()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load playlist"})
		return
	}
	data, _ := os.ReadFile(tmp.Name())

	prefix := keyDir(chunk.HLSPath) // audio/{book}/{page}/hls/
	var b strings.Builder
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if t != "" && !strings.HasPrefix(t, "#") {
			if url, err := store.PresignGet(c.Request.Context(), prefix+t, time.Hour); err == nil {
				b.WriteString(url)
				b.WriteString("\n")
				continue
			}
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	c.Header("Content-Type", "application/vnd.apple.mpegurl")
	c.String(http.StatusOK, b.String())
}

// headHLSHandler answers HEAD /user/books/:book_id/pages/:page/hls.m3u8 — the
// client probes this to decide HLS vs the per-page MP3. Gin doesn't serve HEAD
// on a GET route, so without this the probe always 404s and HLS is never used.
// Cheap: just checks whether the playlist key is set (no R2 round-trip).
func headHLSHandler(c *gin.Context) {
	bookID, _ := strconv.Atoi(c.Param("book_id"))
	pageIndex, _ := strconv.Atoi(c.Param("page"))
	var chunk BookChunk
	if err := db.Where("book_id = ? AND \"index\" = ?", bookID, pageIndex-1).First(&chunk).Error; err != nil || chunk.HLSPath == "" {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Content-Type", "application/vnd.apple.mpegurl")
	c.Status(http.StatusOK)
}

func keyDir(key string) string {
	if i := strings.LastIndex(key, "/"); i >= 0 {
		return key[:i+1]
	}
	return ""
}
