package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/gin-gonic/gin"
)

// signedMediaTTL is the entitlement window for a presigned streaming URL.
const signedMediaTTL = 2 * time.Hour

// serveMedia streams a stored media path to the client. For an R2 object key it
// 302-redirects to a short-lived presigned URL (AVPlayer follows it); for a
// legacy on-disk path it serves the file directly (migration fallback).
func serveMedia(c *gin.Context, stored string) {
	if stored == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "audio not available"})
		return
	}
	if isLegacyLocalPath(stored) {
		if _, err := os.Stat(stored); err == nil {
			c.File(stored)
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "audio file missing on disk"})
		return
	}
	url, err := store.PresignGet(c.Request.Context(), stored, signedMediaTTL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not sign media url"})
		return
	}
	c.Redirect(http.StatusFound, url)
}

// MediaStore abstracts persistent media storage (Cloudflare R2 / any S3).
// FFmpeg and TTS still produce local files; callers PutFile the finished
// artifact and store the returned object key in the DB.
type MediaStore interface {
	PutFile(ctx context.Context, key, localPath, contentType string) error
	GetToFile(ctx context.Context, key, localPath string) error
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	PresignPut(ctx context.Context, key string, ttl time.Duration, contentType string) (string, error)
	Delete(ctx context.Context, key string) error
	// DeletePrefix removes every object under a key prefix. Used to fully
	// clean a book's media tree on delete — final audio, HLS playlists, and
	// the HLS segment files whose names aren't tracked in the DB.
	DeletePrefix(ctx context.Context, prefix string) (int, error)
	Exists(ctx context.Context, key string) (bool, error)
	PublicURL(key string) string
}

// store is the process-wide media store, initialised in main().
var store MediaStore

type r2Store struct {
	client     *s3.Client
	presign    *s3.PresignClient
	bucket     string
	publicBase string
}

// newR2StoreFromEnv builds an R2-backed MediaStore from R2_* env vars.
func newR2StoreFromEnv() (MediaStore, error) {
	accountID := os.Getenv("R2_ACCOUNT_ID")
	accessKey := os.Getenv("R2_ACCESS_KEY_ID")
	secret := os.Getenv("R2_SECRET_ACCESS_KEY")
	bucket := os.Getenv("R2_BUCKET")
	endpoint := os.Getenv("R2_ENDPOINT")
	if endpoint == "" && accountID != "" {
		endpoint = fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID)
	}
	if accessKey == "" || secret == "" || bucket == "" || endpoint == "" {
		return nil, errors.New("R2 not configured (need R2_ACCESS_KEY_ID, R2_SECRET_ACCESS_KEY, R2_BUCKET, and R2_ENDPOINT or R2_ACCOUNT_ID)")
	}

	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("auto"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secret, "")),
	)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
	return &r2Store{
		client:     client,
		presign:    s3.NewPresignClient(client),
		bucket:     bucket,
		publicBase: strings.TrimRight(os.Getenv("R2_PUBLIC_BASE"), "/"),
	}, nil
}

func (s *r2Store) PutFile(ctx context.Context, key, localPath, contentType string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	in := &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Body: f}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}
	_, err = s.client.PutObject(ctx, in)
	return err
}

func (s *r2Store) GetToFile(ctx context.Context, key, localPath string) error {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return err
	}
	defer out.Body.Close()
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, out.Body)
	return err
}

func (s *r2Store) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

// PresignPut returns a short-lived presigned PUT URL. Only Content-Type is
// signed — the client MUST send exactly that Content-Type or R2 rejects the
// PUT with SignatureDoesNotMatch. Objects stay private.
func (s *r2Store) PresignPut(ctx context.Context, key string, ttl time.Duration, contentType string) (string, error) {
	in := &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}
	req, err := s.presign.PresignPutObject(ctx, in, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

func (s *r2Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	return err
}

// DeletePrefix lists and deletes every object under prefix, paginating the
// list and batching deletes (S3 caps DeleteObjects at 1000 keys/call).
// Returns the number of objects removed.
func (s *r2Store) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	if strings.TrimSpace(prefix) == "" {
		return 0, errors.New("DeletePrefix: empty prefix")
	}
	deleted := 0
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket), Prefix: aws.String(prefix),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return deleted, err
		}
		if len(page.Contents) == 0 {
			continue
		}
		ids := make([]types.ObjectIdentifier, 0, len(page.Contents))
		for _, obj := range page.Contents {
			ids = append(ids, types.ObjectIdentifier{Key: obj.Key})
		}
		out, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(s.bucket),
			Delete: &types.Delete{Objects: ids, Quiet: aws.Bool(true)},
		})
		if err != nil {
			return deleted, err
		}
		deleted += len(ids) - len(out.Errors)
	}
	return deleted, nil
}

func (s *r2Store) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		var nf *types.NotFound
		if errors.As(err, &nf) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *r2Store) PublicURL(key string) string {
	if s.publicBase == "" {
		return key
	}
	return s.publicBase + "/" + key
}

// ---- key builders (pure; unit-tested) ----

func audioPageKey(bookID uint, page int, hash, ext string) string {
	return fmt.Sprintf("audio/%d/page_%d_%s%s", bookID, page, shortHash(hash), ext)
}

func groupAudioKey(bookID uint, start, end int) string {
	return fmt.Sprintf("audio/%d/chunks_%d_%d.mp3", bookID, start, end)
}

func bookAudioKey(bookID uint) string {
	return fmt.Sprintf("audio/%d/book.mp3", bookID)
}

func coverKey(bookID uint, hash, ext string) string {
	return fmt.Sprintf("covers/%d/%s%s", bookID, shortHash(hash), ext)
}

func uploadKey(userID, bookID uint, ext string) string {
	return fmt.Sprintf("uploads/%d/%d/original%s", userID, bookID, ext)
}

// isLegacyLocalPath reports whether a stored path is an old on-disk path rather
// than an R2 object key — used by read handlers to serve legacy files during
// the migration window.
func isLegacyLocalPath(p string) bool {
	// R2 keys are relative (audio/…, covers/…, uploads/…, legacy/…); legacy
	// on-disk paths begin with "./" or an absolute "/".
	return strings.HasPrefix(p, "./") || strings.HasPrefix(p, "/")
}

// contentTypeForExt returns a MIME type for a media file extension.
func contentTypeForExt(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".mp3":
		return "audio/mpeg"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".m4a", ".aac":
		return "audio/mp4"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".pdf":
		return "application/pdf"
	case ".epub":
		return "application/epub+zip"
	default:
		return "application/octet-stream"
	}
}

// deleteStored removes a stored media reference: the R2 object for an object
// key, or the local file for a legacy on-disk path. Best-effort (logs only).
func deleteStored(path string) {
	if path == "" {
		return
	}
	if isLegacyLocalPath(path) {
		removeFileIfExists(path)
		return
	}
	if err := store.Delete(context.Background(), path); err != nil {
		log.Printf("⚠️ could not delete R2 object %s: %v", path, err)
	}
}

// uploadArtifact uploads a finished local file to R2 under key, removes the
// local copy on success, and returns the key to store in the DB.
func uploadArtifact(ctx context.Context, localPath, key string) (string, error) {
	if err := store.PutFile(ctx, key, localPath, contentTypeForExt(localPath)); err != nil {
		return "", err
	}
	_ = os.Remove(localPath)
	return key, nil
}

// localizeMedia returns a local filesystem path for a stored media reference.
// For a legacy on-disk path it returns it unchanged; for an R2 key it downloads
// the object to a temp file. The returned cleanup func removes any temp file.
func localizeMedia(ctx context.Context, pathOrKey string) (string, func(), error) {
	noop := func() {}
	if pathOrKey == "" {
		return "", noop, errors.New("empty media path")
	}
	if isLegacyLocalPath(pathOrKey) {
		return pathOrKey, noop, nil
	}
	tmp, err := os.CreateTemp("", "src-*"+filepath.Ext(pathOrKey))
	if err != nil {
		return "", noop, err
	}
	tmp.Close()
	if err := store.GetToFile(ctx, pathOrKey, tmp.Name()); err != nil {
		os.Remove(tmp.Name())
		return "", noop, err
	}
	return tmp.Name(), func() { os.Remove(tmp.Name()) }, nil
}

// legacyKey maps an old local media path to its migrated R2 key
// (legacy/{kind}/{basename}); used by the one-time data migration.
func legacyKey(localPath, kind string) string {
	return "legacy/" + kind + "/" + filepath.Base(localPath)
}
