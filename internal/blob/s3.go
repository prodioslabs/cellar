package blob

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3API is the subset of the S3 client used by Store.
type S3API interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

// S3Store stores objects in an S3-compatible bucket.
type S3Store struct {
	client S3API
	bucket string
	prefix string
}

// NewS3 builds an S3 store from Config using the default AWS credential chain.
func NewS3(ctx context.Context, cfg Config) (*S3Store, error) {
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, fmt.Errorf("s3 bucket is required")
	}
	loadOpts := []func(*config.LoadOptions) error{}
	if cfg.Region != "" {
		loadOpts = append(loadOpts, config.WithRegion(cfg.Region))
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	var clientOpts []func(*s3.Options)
	if cfg.Endpoint != "" {
		clientOpts = append(clientOpts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.UsePathStyle = true
		})
	}
	return &S3Store{
		client: s3.NewFromConfig(awsCfg, clientOpts...),
		bucket: cfg.Bucket,
		prefix: strings.Trim(cfg.Prefix, "/"),
	}, nil
}

// NewS3WithClient is for tests.
func NewS3WithClient(client S3API, bucket, prefix string) *S3Store {
	return &S3Store{client: client, bucket: bucket, prefix: strings.Trim(prefix, "/")}
}

func (s *S3Store) Backend() string { return "s3" }

func (s *S3Store) objectKey(key string) string {
	key = strings.TrimPrefix(key, "/")
	if s.prefix == "" {
		return key
	}
	return s.prefix + "/" + key
}

// Put uploads r as the object body.
func (s *S3Store) Put(ctx context.Context, key string, r io.Reader) error {
	if key == "" {
		return fmt.Errorf("object key is required")
	}
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
		Body:   r,
	})
	return err
}

// Get downloads the object.
func (s *S3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if key == "" {
		return nil, fmt.Errorf("object key is required")
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}

// Delete removes the object.
func (s *S3Store) Delete(ctx context.Context, key string) error {
	if key == "" {
		return fmt.Errorf("object key is required")
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	return err
}

// Open opens a Store from Config. Empty bucket uses local disk.
func Open(ctx context.Context, cfg Config) (Store, error) {
	if strings.TrimSpace(cfg.Bucket) != "" {
		return NewS3(ctx, cfg)
	}
	root := cfg.LocalRoot()
	if root == "" {
		return nil, fmt.Errorf("data-dir or snapshot-dir is required for local snapshots")
	}
	return NewLocal(root)
}
