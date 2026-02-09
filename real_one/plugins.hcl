plugin "playwright" {
  source  = "github.com/mlund01/plugin_playwright"
  version = "v0.0.2"

  settings {
    provider     = "local"
    headless     = false
    browser_type = "chromium"
  }
}
