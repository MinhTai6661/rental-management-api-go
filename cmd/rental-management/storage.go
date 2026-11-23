package main

import (
	"fmt"

	"rental-management/config"
	"rental-management/pkg/storage"
)

// openStorage builds the picture store chosen by STORAGE_DRIVER. The returned function releases it.
func openStorage(cfg config.Config) (storage.Storage, func(), error) {
	switch cfg.StorageDriver {
	case "s3":
		s3, err := newS3Storage(cfg)
		if err != nil {
			return nil, nil, err
		}
		return s3, func() {}, nil
	case "local", "":
		disk, err := storage.NewLocalDisk(cfg.MediaDir, cfg.MediaPublicBaseURL)
		if err != nil {
			return nil, nil, err
		}
		return disk, func() { _ = disk.Close() }, nil
	default:
		return nil, nil, fmt.Errorf("unknown STORAGE_DRIVER %q", cfg.StorageDriver)
	}
}

// newS3Storage builds the S3 driver from the S3_* settings.
func newS3Storage(cfg config.Config) (*storage.S3, error) {
	return storage.NewS3(storage.S3Config{
		Endpoint:        cfg.S3Endpoint,
		Bucket:          cfg.S3Bucket,
		AccessKeyID:     cfg.S3AccessKeyID,
		SecretAccessKey: cfg.S3SecretAccessKey,
		Region:          cfg.S3Region,
		PathStyle:       cfg.S3PathStyle,
		PublicBaseURL:   cfg.MediaPublicBaseURL,
	})
}
