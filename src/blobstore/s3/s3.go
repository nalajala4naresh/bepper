// Package s3 implements blobstore.Blobstore against an S3-compatible
// bucket, adapted from BuildBuddy's AWS S3 blobstore (MIT licensed):
// https://github.com/buildbuddy-io/buildbuddy/blob/master/server/backends/blobstore/aws/aws.go
package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/nalajala4naresh/bepper/src/blobstore"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	s3manager "github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	s3transfermanager "github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const bucketWaitTimeout = 10 * time.Second

// Config configures a BlobStore.
type Config struct {
	// Bucket is the S3 bucket to store blobs in. Required.
	Bucket string
	// Region is the AWS region the bucket lives in.
	Region string
	// Endpoint overrides the S3 endpoint, for S3-compatible services like
	// MinIO or LocalStack. Optional.
	Endpoint string
}

// BlobStore stores blobs as gzip-compressed objects in an S3 bucket.
// Credentials are resolved via the standard AWS SDK credential chain (env
// vars, shared config/profile, or an attached IAM role).
type BlobStore struct {
	client     *s3.Client
	bucket     string
	downloader *s3transfermanager.Client
	uploader   *s3manager.Uploader
}

// New creates a BlobStore, creating the configured bucket if it doesn't
// already exist.
func New(ctx context.Context, cfg Config) (*BlobStore, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("s3: bucket is required")
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = awssdk.String(cfg.Endpoint)
			o.UsePathStyle = true
		}
	})

	store := &BlobStore{
		client:     client,
		bucket:     cfg.Bucket,
		downloader: s3transfermanager.New(client),
		uploader:   s3manager.NewUploader(client),
	}

	if err := store.createBucketIfNotExists(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *BlobStore) bucketExists(ctx context.Context) (bool, error) {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &s.bucket})
	if err == nil {
		return true, nil
	}
	var nf *s3types.NotFound
	if errors.As(err, &nf) {
		return false, nil
	}
	return false, err
}

func (s *BlobStore) createBucketIfNotExists(ctx context.Context) error {
	exists, err := s.bucketExists(ctx)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &s.bucket}); err != nil {
		return fmt.Errorf("create bucket %q: %w", s.bucket, err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, bucketWaitTimeout)
	defer cancel()
	return s3.NewBucketExistsWaiter(s.client).Wait(waitCtx, &s3.HeadBucketInput{Bucket: &s.bucket}, bucketWaitTimeout)
}

func (s *BlobStore) WriteBlob(ctx context.Context, blobName string, data []byte) (int, error) {
	compressed, err := blobstore.Compress(data)
	if err != nil {
		return 0, err
	}
	if _, err := s.uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket: &s.bucket,
		Key:    &blobName,
		Body:   bytes.NewReader(compressed),
	}); err != nil {
		return 0, err
	}
	return len(compressed), nil
}

func (s *BlobStore) ReadBlob(ctx context.Context, blobName string) ([]byte, error) {
	buf := &s3manager.WriteAtBuffer{}
	if _, err := s.downloader.DownloadObject(ctx, &s3transfermanager.DownloadObjectInput{Bucket: &s.bucket, Key: &blobName, WriterAt: buf}); err != nil {
		var nsk *s3types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, fmt.Errorf("blob %q not found: %w", blobName, err)
		}
		return nil, err
	}
	return blobstore.Decompress(buf.Bytes(), nil)
}

func (s *BlobStore) DeleteBlob(ctx context.Context, blobName string) error {
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &blobName}); err != nil {
		return err
	}
	waitCtx, cancel := context.WithTimeout(ctx, bucketWaitTimeout)
	defer cancel()
	return s3.NewObjectNotExistsWaiter(s.client).Wait(waitCtx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &blobName}, bucketWaitTimeout)
}

func (s *BlobStore) BlobExists(ctx context.Context, blobName string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &blobName})
	if err != nil {
		var nf *s3types.NotFound
		if errors.As(err, &nf) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Writer streams blobName's contents to S3 via a multipart upload, so
// callers don't need to buffer the whole blob in memory. The object is only
// visible in the bucket once Close returns without error.
func (s *BlobStore) Writer(ctx context.Context, blobName string) (io.WriteCloser, error) {
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)

	go func() {
		_, err := s.uploader.Upload(ctx, &s3.PutObjectInput{
			Bucket: &s.bucket,
			Key:    &blobName,
			Body:   pr,
		})
		errCh <- err
	}()

	return newStreamWriter(pw, errCh), nil
}
