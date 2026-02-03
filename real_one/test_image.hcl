workflow "test_image" {
  supervisor_model = models.anthropic.claude_sonnet_4
  agents           = [agents.browser_navigator]

  input "url" {
    type    = "string"
    default = "https://example.com"
  }

  task "analyze_page" {
    objective = <<-EOT
      1. Navigate to ${inputs.url}
      2. Take a screenshot of the page
      3. Describe what you see in the screenshot - the layout, content, colors, and any notable elements
    EOT

    output {
      field "description" {
        type        = "string"
        description = "Description of what was seen in the screenshot"
        required    = true
      }
    }
  }
}
