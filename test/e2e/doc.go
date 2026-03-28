// Package e2e contains end-to-end integration tests.
//
// Live tests (build tag e2e_live) expect:
//   - KIMI_API_KEY: required, used for real Moonshot HTTP requests.
//   - KIMI_BASE_URL: optional, defaults to Moonshot API base.
//   - KIMI_MODEL: optional, when empty the tests resolve a model via /models.
//   - OPENAI_API_KEY: optional, enables OpenAI live provider tests.
//   - OPENAI_BASE_URL / OPENAI_MODEL: optional OpenAI overrides.
//   - ANTHROPIC_API_KEY: optional, enables Anthropic live provider tests.
//   - ANTHROPIC_BASE_URL / ANTHROPIC_MODEL: optional Anthropic overrides.
//   - GEMINI_API_KEY: optional, enables Gemini live provider tests.
//   - GEMINI_BASE_URL / GEMINI_MODEL: optional Gemini overrides.
package e2e
