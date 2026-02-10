mission "secrets_test" {
  supervisor_model = models.openai.gpt_4o
  agents           = [agents.assistant]

  # Secret input from a global variable
  input "api_token" {
    type        = "string"
    secret      = true
    value       = vars.airtable_password
    description = "API token for authentication (use as Bearer token)"
  }

  # Secret input with discrete/hardcoded value
  input "static_key" {
    type        = "string"
    secret      = true
    value       = "sk-test-12345"
    description = "A static API key (use in X-API-Key header)"
  }

  task "test_secrets" {
    objective = <<-EOT
      Make a simple HTTP GET request to https://httpbin.org/headers using the http.get tool.
      Include an Authorization header using the api_token secret as a Bearer token.
      Also include an X-API-Key header using the static_key secret.
      Report back what headers the server received.
    EOT
  }
}
