// Package s3x browses S3 buckets and fetches objects for previews.
package s3x

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/auth"
)

type Bucket struct {
	Name, Region string
	Created      time.Time
}

// BucketsPage lists up to 200 buckets (with their region when S3 returns it).
func BucketsPage(ctx context.Context, s *auth.Session, token string) ([]Bucket, string, error) {
	in := &sdk.ListBucketsInput{MaxBuckets: aws.Int32(200)}
	if token != "" {
		in.ContinuationToken = aws.String(token)
	}
	out, err := s.S3().ListBuckets(ctx, in)
	if err != nil {
		return nil, "", err
	}
	bs := make([]Bucket, 0, len(out.Buckets))
	for _, b := range out.Buckets {
		bs = append(bs, Bucket{Name: aws.ToString(b.Name), Region: aws.ToString(b.BucketRegion), Created: aws.ToTime(b.CreationDate)})
	}
	return bs, aws.ToString(out.ContinuationToken), nil
}

// Region resolves a bucket's region.
func Region(ctx context.Context, s *auth.Session, bucket string) (string, error) {
	out, err := s.S3().GetBucketLocation(ctx, &sdk.GetBucketLocationInput{Bucket: aws.String(bucket)})
	if err != nil {
		return "", err
	}
	switch loc := string(out.LocationConstraint); loc {
	case "":
		return "us-east-1", nil
	case "EU":
		return "eu-west-1", nil
	default:
		return loc, nil
	}
}

// Security summarises public-access blocking and default encryption.
type Security struct {
	PublicBlocked bool // all four public access block settings on
	BlockKnown    bool
	Encryption    string // e.g. "aws:kms", "AES256", "" = unknown/none
}

func BucketSecurity(ctx context.Context, s *auth.Session, bucket, region string) Security {
	c := s.S3For(region)
	var sec Security
	if out, err := c.GetPublicAccessBlock(ctx, &sdk.GetPublicAccessBlockInput{Bucket: aws.String(bucket)}); err == nil && out.PublicAccessBlockConfiguration != nil {
		p := out.PublicAccessBlockConfiguration
		sec.BlockKnown = true
		sec.PublicBlocked = aws.ToBool(p.BlockPublicAcls) && aws.ToBool(p.BlockPublicPolicy) && aws.ToBool(p.IgnorePublicAcls) && aws.ToBool(p.RestrictPublicBuckets)
	}
	if out, err := c.GetBucketEncryption(ctx, &sdk.GetBucketEncryptionInput{Bucket: aws.String(bucket)}); err == nil && out.ServerSideEncryptionConfiguration != nil {
		for _, r := range out.ServerSideEncryptionConfiguration.Rules {
			if d := r.ApplyServerSideEncryptionByDefault; d != nil {
				sec.Encryption = string(d.SSEAlgorithm)
			}
		}
	}
	return sec
}

type Entry struct {
	Key, Name    string // Name is the part after the prefix
	Folder       bool
	Size         int64
	Modified     time.Time
	StorageClass string
}

// ListPage lists one level under prefix (folders first), 200 keys per page.
func ListPage(ctx context.Context, s *auth.Session, region, bucket, prefix, token string) ([]Entry, string, error) {
	in := &sdk.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(prefix), Delimiter: aws.String("/"), MaxKeys: aws.Int32(200)}
	if token != "" {
		in.ContinuationToken = aws.String(token)
	}
	out, err := s.S3For(region).ListObjectsV2(ctx, in)
	if err != nil {
		return nil, "", err
	}
	var es []Entry
	for _, p := range out.CommonPrefixes {
		k := aws.ToString(p.Prefix)
		es = append(es, Entry{Key: k, Name: strings.TrimPrefix(k, prefix), Folder: true})
	}
	for _, o := range out.Contents {
		k := aws.ToString(o.Key)
		if k == prefix {
			continue // the "folder" placeholder object
		}
		es = append(es, Entry{Key: k, Name: strings.TrimPrefix(k, prefix), Size: aws.ToInt64(o.Size),
			Modified: aws.ToTime(o.LastModified), StorageClass: string(o.StorageClass)})
	}
	next := ""
	if aws.ToBool(out.IsTruncated) {
		next = aws.ToString(out.NextContinuationToken)
	}
	return es, next, nil
}

type Head struct {
	Size        int64
	ContentType string
	Modified    time.Time
	Archived    bool // GLACIER / DEEP_ARCHIVE objects need a restore first
}

func HeadObject(ctx context.Context, s *auth.Session, region, bucket, key string) (Head, error) {
	out, err := s.S3For(region).HeadObject(ctx, &sdk.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return Head{}, err
	}
	h := Head{Size: aws.ToInt64(out.ContentLength), ContentType: aws.ToString(out.ContentType), Modified: aws.ToTime(out.LastModified)}
	switch out.StorageClass {
	case types.StorageClassGlacier, types.StorageClassDeepArchive:
		h.Archived = out.Restore == nil
	}
	return h, nil
}

// ReadRange returns at most n bytes from the start of an object.
func ReadRange(ctx context.Context, s *auth.Session, region, bucket, key string, n int64) ([]byte, error) {
	out, err := s.S3For(region).GetObject(ctx, &sdk.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key),
		Range: aws.String(fmt.Sprintf("bytes=0-%d", n-1))})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	return io.ReadAll(io.LimitReader(out.Body, n))
}

var ErrTooLarge = errors.New("object is larger than the preview limit")

// Download saves an object to a private temp file (at most max bytes).
func Download(ctx context.Context, s *auth.Session, region, bucket, key string, max int64) (string, error) {
	out, err := s.S3For(region).GetObject(ctx, &sdk.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return "", err
	}
	defer out.Body.Close()
	if aws.ToInt64(out.ContentLength) > max {
		return "", ErrTooLarge
	}
	f, err := os.CreateTemp(TempDir(), "preview-*"+path.Ext(key))
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, io.LimitReader(out.Body, max)); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// TempDir is where previews are written (cleaned at startup and on close).
func TempDir() string {
	d := path.Join(os.TempDir(), "devops-preview")
	_ = os.MkdirAll(d, 0o700)
	return d
}

// CleanTemp removes preview files older than an hour.
func CleanTemp() {
	entries, err := os.ReadDir(TempDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > time.Hour {
			_ = os.Remove(path.Join(TempDir(), e.Name()))
		}
	}
}

// Presign returns a GET URL valid for five minutes.
func Presign(ctx context.Context, s *auth.Session, region, bucket, key string) (string, error) {
	pc := sdk.NewPresignClient(s.S3For(region))
	req, err := pc.PresignGetObject(ctx, &sdk.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}, sdk.WithPresignExpires(5*time.Minute))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}
