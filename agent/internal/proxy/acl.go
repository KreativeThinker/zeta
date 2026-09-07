package proxy

// isWildcard reports whether an ACL list grants access to any enrolled mesh peer.
func isWildcard(pks []string) bool {
	for _, pk := range pks {
		if pk == "*" {
			return true
		}
	}
	return false
}

func setOf(keys []string) map[string]struct{} {
	m := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		m[k] = struct{}{}
	}
	return m
}
