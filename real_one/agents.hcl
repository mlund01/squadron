agent "assistant" {
  model       = models.openai.gpt_4o
  personality = "Friendly, helpful, and concise. Enjoys solving problems and explaining complex topics in simple terms."
  role        = "A general-purpose assistant that helps users with questions, tasks, and creative projects."
  tools       = [plugins.bash.bash, plugins.http.get, tools.weather]
}

agent "browser_navigator" {
  model       = models.openai.gpt_4o
  personality = "Methodical and precise. Carefully navigates web pages and extracts information accurately."
  role        = "A browser automation specialist that navigates websites, interacts with elements, and extracts content. While you have access to all Playwright tools, prefer using browser_aria_snapshot (for understanding page structure), browser_screenshot (for visual confirmation), and browser_click_coordinates (for reliable clicking) - these are optimized for LLM-based browser automation."
  tools       = [plugins.playwright.all]

  pruning {
    single_tool_limit = 1
    all_tool_limit    = 4
  }
}

agent "compaction_test" {
  model       = models.openai.gpt_4o
  personality = "Helpful assistant for testing compaction."
  role        = "A test agent with a very low compaction threshold to verify context compaction works correctly."
  tools       = [plugins.bash.bash, plugins.http.get]

  # No pruning block = all pruning disabled (defaults are 0)

  compaction {
    token_limit    = 3000  # Very low threshold for testing
    turn_retention = 2     # Keep last 2 turns intact
  }
}

agent "turn_limit_test" {
  model       = models.openai.gpt_4o
  personality = "Helpful assistant for testing turn limit pruning."
  role        = "A test agent with a low turn limit to verify rolling window pruning works correctly."
  tools       = [plugins.bash.bash, plugins.http.get]

  pruning {
    turn_limit = 3  # Keep only last 3 turns (6 messages)
  }
}

