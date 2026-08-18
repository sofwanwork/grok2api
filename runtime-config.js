window.__GROK2API_RUNTIME_CONFIG__ = {
  apiBaseUrl: "",
  publicApiBaseUrl: ""
};

// Force English UI by default so dashboard does not fall back to zh-CN.
// Runs before the SPA boots. Manual language switch still works in-session;
// full page reload returns to English unless user re-selects Chinese.
try {
  window.localStorage.setItem("grok2api:language", "en");
} catch (e) {
  // ignore storage failures
}
