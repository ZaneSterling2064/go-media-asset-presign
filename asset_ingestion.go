package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
)

const maxSourceBytes int64 = 2 << 30

var identifierPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type assetStorage interface {
	presign(context.Context, string, string, presignRequest) (presignResult, error)
	head(context.Context, string, string) (headResult, error)
}

type assetPipeline struct {
	storage assetStorage
	bucket  string
}

type uploadIntent struct {
	CreatorID   string `json:"creator_id"`
	AssetID     string `json:"asset_id"`
	ContentType string `json:"content_type"`
	Bytes       int64  `json:"bytes"`
}

type uploadPlan struct {
	AssetID       string `json:"asset_id"`
	ObjectKey     string `json:"object_key"`
	UploadURL     string `json:"upload_url"`
	UploadMethod  string `json:"upload_method"`
	ProcessingJob string `json:"processing_job"`
	State         string `json:"state"`
}

func (p assetPipeline) planUpload(ctx context.Context, input uploadIntent) (uploadPlan, error) {
	if !identifierPattern.MatchString(input.CreatorID) || !identifierPattern.MatchString(input.AssetID) {
		return uploadPlan{}, errors.New("creator_id and asset_id must be 1-64 letters, digits, underscores, or hyphens")
	}
	if input.ContentType != "video/mp4" && input.ContentType != "audio/mpeg" {
		return uploadPlan{}, errors.New("content_type must be video/mp4 or audio/mpeg")
	}
	if input.Bytes <= 0 || input.Bytes > maxSourceBytes {
		return uploadPlan{}, fmt.Errorf("bytes must be between 1 and %d", maxSourceBytes)
	}

	key := input.CreatorID + "/" + input.AssetID + "/source"
	jobID := stableID("process", input.CreatorID, input.AssetID)
	signed, err := p.storage.presign(ctx, p.bucket, key, presignRequest{
		Op:             "put",
		ExpiresSeconds: 600,
		ContentType:    input.ContentType,
		MaxBytes:       input.Bytes,
		IdempotencyKey: jobID,
	})
	if err != nil {
		return uploadPlan{}, fmt.Errorf("%s: %w", canonicalOperation, err)
	}
	return uploadPlan{
		AssetID:       input.AssetID,
		ObjectKey:     key,
		UploadURL:     signed.URL,
		UploadMethod:  "PUT",
		ProcessingJob: jobID,
		State:         "upload_pending",
	}, nil
}

type delivery struct {
	AssetID     string `json:"asset_id"`
	State       string `json:"state"`
	DeliveryURL string `json:"delivery_url,omitempty"`
}

func (p assetPipeline) creatorDelivery(ctx context.Context, creatorID, assetID string) (delivery, error) {
	if !identifierPattern.MatchString(creatorID) || !identifierPattern.MatchString(assetID) {
		return delivery{}, errors.New("invalid creator or asset identifier")
	}
	key := creatorID + "/" + assetID + "/stream.mp4"
	object, err := p.storage.head(ctx, p.bucket, key)
	if err != nil {
		return delivery{}, err
	}
	if !object.Found {
		return delivery{AssetID: assetID, State: "processing"}, nil
	}
	signed, err := p.storage.presign(ctx, p.bucket, key, presignRequest{
		Op:                  "get",
		ExpiresSeconds:      300,
		ResponseDisposition: "inline",
	})
	if err != nil {
		return delivery{}, err
	}
	return delivery{AssetID: assetID, State: "ready", DeliveryURL: signed.URL}, nil
}

func stableID(parts ...string) string {
	digest := sha256.Sum256([]byte(parts[0] + ":" + parts[1] + ":" + parts[2]))
	return parts[0] + "_" + hex.EncodeToString(digest[:8])
}
