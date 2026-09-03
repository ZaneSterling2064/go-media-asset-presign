package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeStorage struct {
	presignResult presignResult
	presignInput  presignRequest
	presignCalls  int
	headResult    headResult
}

func TestHeadReadsMissingObjectState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/storage/object/head/assets/creator-7%2Fcut%2Fstream.mp4" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.EscapedPath())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"found":false,"metadata":{}}`))
	}))
	defer server.Close()

	client := newStorageClient(server.URL, "test-key", server.Client())
	got, err := client.head(context.Background(), "assets", "creator-7/cut/stream.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if got.Found {
		t.Fatal("missing object reported as found")
	}
}

func (f *fakeStorage) presign(_ context.Context, _, _ string, input presignRequest) (presignResult, error) {
	f.presignCalls++
	f.presignInput = input
	return f.presignResult, nil
}

func (f *fakeStorage) head(context.Context, string, string) (headResult, error) {
	return f.headResult, nil
}

func TestPlanUploadDecision(t *testing.T) {
	tests := []struct {
		name      string
		input     uploadIntent
		wantError bool
		wantCalls int
		wantState string
	}{
		{
			name:      "accepted video becomes a pending upload and processing job",
			input:     uploadIntent{CreatorID: "creator-7", AssetID: "launch-cut", ContentType: "video/mp4", Bytes: 8_000_000},
			wantCalls: 1,
			wantState: "upload_pending",
		},
		{
			name:      "oversized source is rejected before signing",
			input:     uploadIntent{CreatorID: "creator-7", AssetID: "raw-cut", ContentType: "video/mp4", Bytes: maxSourceBytes + 1},
			wantError: true,
		},
		{
			name:      "non-media input is rejected before signing",
			input:     uploadIntent{CreatorID: "creator-7", AssetID: "notes", ContentType: "text/plain", Bytes: 100},
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storage := &fakeStorage{presignResult: presignResult{URL: "https://upload.example/signed"}}
			pipeline := assetPipeline{storage: storage, bucket: "assets"}
			got, err := pipeline.planUpload(context.Background(), test.input)
			if test.wantError && err == nil {
				t.Fatal("expected an error")
			}
			if !test.wantError && err != nil {
				t.Fatal(err)
			}
			if storage.presignCalls != test.wantCalls {
				t.Fatalf("presign calls = %d, want %d", storage.presignCalls, test.wantCalls)
			}
			if !test.wantError {
				if got.State != test.wantState || got.ProcessingJob == "" {
					t.Fatalf("unexpected plan: %+v", got)
				}
				if storage.presignInput.Op != "put" || storage.presignInput.MaxBytes != test.input.Bytes {
					t.Fatalf("signing boundary lost constraints: %+v", storage.presignInput)
				}
			}
		})
	}
}

func TestCreatorDeliveryUsesFoundState(t *testing.T) {
	tests := []struct {
		name      string
		found     bool
		wantState string
		wantCalls int
	}{
		{name: "rendition absent", found: false, wantState: "processing", wantCalls: 0},
		{name: "rendition present", found: true, wantState: "ready", wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storage := &fakeStorage{headResult: headResult{Found: test.found}, presignResult: presignResult{URL: "https://delivery.example/signed"}}
			got, err := (assetPipeline{storage: storage, bucket: "assets"}).creatorDelivery(context.Background(), "creator-7", "launch-cut")
			if err != nil {
				t.Fatal(err)
			}
			if got.State != test.wantState || storage.presignCalls != test.wantCalls {
				t.Fatalf("delivery = %+v, calls = %d", got, storage.presignCalls)
			}
		})
	}
}
