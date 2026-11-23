//go:build integration

package storage

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestS3RealEndpoint runs the Storage contract against a real S3 API. It is skipped unless S3_TEST_ENDPOINT,
// S3_TEST_BUCKET, S3_TEST_ACCESS_KEY_ID and S3_TEST_SECRET_ACCESS_KEY are set (S3_TEST_REGION, S3_TEST_PATH_STYLE
// optional). Objects are written under a "property/" key and removed again.
func TestS3RealEndpoint(t *testing.T) {
	endpoint, bucket := os.Getenv("S3_TEST_ENDPOINT"), os.Getenv("S3_TEST_BUCKET")
	keyID, secret := os.Getenv("S3_TEST_ACCESS_KEY_ID"), os.Getenv("S3_TEST_SECRET_ACCESS_KEY")
	if endpoint == "" || bucket == "" || keyID == "" || secret == "" {
		t.Skip("S3_TEST_ENDPOINT, S3_TEST_BUCKET, S3_TEST_ACCESS_KEY_ID and S3_TEST_SECRET_ACCESS_KEY are not set")
	}
	region := os.Getenv("S3_TEST_REGION")
	if region == "" {
		region = "us-east-1"
	}
	s, err := NewS3(S3Config{
		Endpoint: endpoint, Bucket: bucket, AccessKeyID: keyID, SecretAccessKey: secret, Region: region,
		PathStyle: !strings.EqualFold(os.Getenv("S3_TEST_PATH_STYLE"), "false"), PublicBaseURL: "http://cdn.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = s.Delete(context.Background(), "property/2026/11/aaa.webp", "property/2026/11/bbb.webp")
	})
	run(t, s)
}
