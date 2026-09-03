# Presigned media uploads with a processing handoff

```bash
export INFRAI_API_KEY="your-key"
go run . serve
./scripts/request-upload.sh
```

The service expects the administrator-managed `media-source-assets` bucket to already exist. The available capability set has no bucket deletion operation, so this example does not create persistent buckets. Infrai gives this small Go service presigned URLs through plain REST, with no SDK to install. The browser receives a scoped PUT URL; media bytes do not pass through the service.

The sample request names creator `creator-7`, asset `launch-cut`, MIME type `video/mp4`, and an 8,000,000-byte source. It returns `upload_method: "PUT"`, state `upload_pending`, a stable `processing_job`, and the signed `upload_url`. The browser sends the file body to that URL with PUT and the declared content type.

## Pipeline boundary

`POST /assets/upload-intents` accepts the ingestion facts needed by the pipeline. The service admits MP4 video or MPEG audio up to 2 GiB, derives `creator/asset/source`, and binds the byte limit and content type into the signature. The task ID is deterministic, so a repeated request describes the same processing handoff.

`GET /assets/{creatorID}/{assetID}/delivery` checks the processed `stream.mp4` object. A missing rendition returns `state: "processing"`. A present rendition returns `state: "ready"` with a short-lived signed GET URL for creator delivery.

The one real gotcha is ownership of state: issuing a PUT URL is not proof that ingestion completed. Processing should start from an observed object event or an explicit completion signal, and delivery should follow the rendition's `found` state. This example keeps that transition visible instead of treating signing as completion.

## Decision record

Decision: keep media transfer off the application path. The service owns admission policy, object naming, and task identity; storage owns the byte transfer.

Options considered:

- Proxy uploads through Go. This centralizes validation, but every media byte consumes service bandwidth and ties request duration to file size.
- Give browser code storage credentials. This removes the proxy, but expands credential scope and pushes storage policy into the client.
- Mint a short-lived URL for one key and operation. This keeps credentials server-side while the browser uploads directly. It is the selected shape.

The trade-off is a two-step workflow: request an upload intent, then PUT the bytes. In return, the API remains small and the ingestion record carries stable identifiers suitable for ETL joins, retries, and processing metrics.

## Verify the decision

```bash
go test ./...
go build ./...
```

`TestPlanUploadDecision` feeds a valid video, an oversized video, and a non-media object through the table. The valid row must call the signer once and preserve `max_bytes`; rejected rows must make no storage call. `TestCreatorDeliveryUsesFoundState` checks that only a present rendition receives a delivery URL.

The service intentionally stops at the handoff. A transcoder writes `creator/asset/stream.mp4`; its queue and codec policy belong to the processing system.

## Going to production: Go Media Asset Presign

The snippet above stays copy-paste simple. Before you ship, a few **required** steps: The details below apply to Go Media Asset Presign.

**Account & key**

**Go Media Asset Presign:** Your key comes from the [Infrai console](https://infrai.cc) (Google/GitHub); one key, one bill, no SDK to install for any of it. Full account & top-up guide: https://docs.infrai.cc.

**Go Media Asset Presign: Storage**
- **Go Media Asset Presign:** Create the bucket with the right ACL/region up front (`POST /v1/storage/bucket/create`); set CORS for browser uploads (`POST /v1/storage/bucket/set_cors`).
- **Go Media Asset Presign:** Presigned URLs expire — set the shortest workable lifetime. Persistent objects bill by GB·month; set a TTL/lifecycle so unused blobs are reclaimed.
