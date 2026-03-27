// Package e2e contains end-to-end integration tests.
//
// Live tests (build tag e2e_live) expect:
//   - KIMI_API_KEY: required, used for real Moonshot HTTP requests.
//   - KIMI_BASE_URL: optional, defaults to Moonshot API base.
//   - KIMI_MODEL: optional, when empty the tests resolve a model via /models.
package e2e
