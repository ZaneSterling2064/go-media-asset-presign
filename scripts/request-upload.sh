#!/bin/sh
set -eu

curl --fail-with-body \
  --request POST \
  --header 'Content-Type: application/json' \
  --data '{"creator_id":"creator-7","asset_id":"launch-cut","content_type":"video/mp4","bytes":8000000}' \
  http://localhost:8080/assets/upload-intents
