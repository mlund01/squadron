plugin "pinger" {
  source  = "~/.squad/plugins/pinger"
  version = "local"
}

plugin "playwright" {
  source  = "~/.squad/plugins/playwright"
  version = "local"

  settings {
    provider     = "local"
    headless     = true
    browser_type = "chromium"
  }
}
