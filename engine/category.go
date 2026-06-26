package engine

// Category groups an engine type for display in `doze status` / the dash, so the
// instance list reads by concern (databases, caches, queues, storage, your
// services) instead of as a flat dump. This is a stopgap static map; once engines
// are out-of-process modules they will declare their own category.
func Category(engineType string) string {
	switch engineType {
	case "postgres", "documentdb", "mysql":
		return "database"
	case "valkey", "kvrocks", "redis":
		return "cache"
	case "sqs", "sns", "kafka":
		return "queue"
	case "s3", "minio":
		return "storage"
	case "process":
		return "services"
	default:
		return "other"
	}
}

// CategoryOrder is the canonical display order of categories.
var CategoryOrder = []string{"database", "cache", "queue", "storage", "services", "other"}
