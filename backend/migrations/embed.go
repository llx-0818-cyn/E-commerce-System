package migrations

import "embed"

// Files 包含随 API、Worker 和 migration CLI 一同发布的版本化 SQL。
//
//go:embed *.sql
var Files embed.FS
