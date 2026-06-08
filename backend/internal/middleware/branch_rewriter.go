package middleware

import (
	"net/http"
	"strings"
)

// ReservedBranchOperations define terms that should not be treated as branch slugs
var ReservedBranchOperations = map[string]bool{
	"status":      true,
	"diff":        true,
	"conflicts":   true,
	"list":        true,
	"get":         true,
	"create":      true,
	"update":      true,
	"delete":      true,
	"deploy":      true,
	"ensure-main": true,
	"access":      true,
}

// BranchRewriterInterceptor acts as an intelligent pre-router.
// It detects URLs containing "/branch/{branch_slug}", extracts the branch slug,
// sets the X-Cascata-Env header, and rewrites the URL so the standard routing
// works seamlessly without having to duplicate every single route.
func BranchRewriterInterceptor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		
		// Look for "/branch/" in the path
		idx := strings.Index(path, "/branch/")
		if idx != -1 {
			// Extract the part after "/branch/"
			afterBranch := path[idx+len("/branch/"):]
			
			// Find the next slash to isolate the branch slug
			nextSlash := strings.Index(afterBranch, "/")
			var branchSlug string
			var restOfPath string
			
			if nextSlash == -1 {
				branchSlug = afterBranch
				restOfPath = ""
			} else {
				branchSlug = afterBranch[:nextSlash]
				restOfPath = afterBranch[nextSlash:]
			}
			
			// Check if it's a valid branch slug and not a reserved operation
			if branchSlug != "" && !ReservedBranchOperations[branchSlug] {
				// Clone the request to avoid race conditions
				r = r.Clone(r.Context())

				// Inject the branch slug as a header so ProjectResolver can pick it up
				r.Header.Set("X-Cascata-Env", branchSlug)
				
				// Rewrite the URL by removing the "/branch/{branch_slug}" part
				r.URL.Path = path[:idx] + restOfPath
				if r.URL.Path == "" {
					r.URL.Path = "/"
				}
			}
		}
		
		next.ServeHTTP(w, r)
	})
}
