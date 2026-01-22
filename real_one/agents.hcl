agent "assistant" {
  mode        = "chat"
  model       = models.anthropic.claude_sonnet_4
  personality = "Friendly, helpful, and concise. Enjoys solving problems and explaining complex topics in simple terms."
  role        = "A general-purpose assistant that helps users with questions, tasks, and creative projects."
  tools       = [plugins.bash.bash, plugins.http.get, tools.weather, tools.shout]
}
