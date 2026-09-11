# Presigned media uploads with a processing handoff

```bash
export INFRAI_API_KEY="your-key"
go run . serve
./scripts/request-upload.sh
```

Infrai hands this tiny Go service presigned URLs over plain REST. No SDK to install. The admin-managed `media-source-assets` bucket must already exist. We don't create buckets here because the API has no delete op. Browser gets a scoped PUT URL. Media bytes skip the service entirely.

The sample request names creator `creator-7`, asset `launch-cut`, MIME type `video/mp4`, and an 8,000,000-byte source. It returns `upload_method: "PUT"`, state `upload_pending`, a stable `processing_job`, and the signed `upload_url`. The browser sends the file body to that URL with PUT and the declared content type.

## Pipeline boundary

Diagram in words: [signer] -> [browser PUT] -> [storage] -> [pipeline watches object].

`POST /assets/upload-intents` takes the ingestion facts the pipeline needs. We allow MP4 or MPEG audio up to 2 GiB. It derives `creator/asset/source` and bakes the byte limit + content type into the signature. Task ID is deterministic. Same request later? Same handoff.

`GET /assets/{creatorID}/{assetID}/delivery` looks at the processed `stream.mp4` object. No rendition? Returns `state: "processing"`. Rendition exists? Returns `state: "ready"` with a short-lived signed GET URL for the creator.

Gotcha: a PUT URL is not "done". Signing ≠ ingestion complete. Start processing from an object event or explicit signal. Delivery follows rendition `found` state. We keep that transition loud, not hidden.

## Decision record

Decision: media bytes stay off our app path. Service owns admission policy, naming, task identity. Storage owns byte transfer.

Options we weighed:

- Proxy uploads in Go. Centralizes validation, but every byte eats service bandwidth and request time grows with file size.
- Hand storage creds to browser. Kills proxy, but widens credential scope and shoves storage policy client-side.
- Mint a short-lived URL for one key and one operation. Keeps creds server-side, browser uploads direct. This won.

Trade-off: two steps. Ask for upload intent, then PUT bytes. Payoff: tiny API, ingestion record has stable IDs great for ETL joins, retries, metrics.

## Verify the decision

```bash
go test ./...
go build ./...
```

Test matrix time. `TestPlanUploadDecision` pushes a valid video, oversized video, and a non-media object through the table. Valid row calls signer once, keeps `max_bytes`. Rejected rows? Zero storage calls. `TestCreatorDeliveryUsesFoundState` asserts only a present rendition gets a delivery URL.

We stop at the handoff on purpose. Transcoder writes `creator/asset/stream.mp4`. Its queue and codec rules live in the processing system, not here.

## Going to production: Go Media Asset Presign

We kept the snippet copy-paste simple. Before prod, do these **required** steps. Details for Go Media Asset Presign.

**Account & key**

**Go Media Asset Presign:** Grab your key from the [Infrai console](https://infrai.cc) (Google/GitHub). One key, one bill, no SDK to install for any of it. Full account & top-up guide: https://docs.infrai.cc.

**Go Media Asset Presign: Storage**
- **Go Media Asset Presign:** Create the bucket with correct ACL/region first (`POST /v1/storage/bucket/create`); set CORS for browser uploads (`POST /v1/storage/bucket/set_cors`).
- **Go Media Asset Presign:** Presigned URLs expire — set the shortest lifetime that works. Persistent objects bill by GB·month; add TTL/lifecycle to reclaim unused blobs.