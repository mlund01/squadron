workflow "midwest_weather" {
  supervisor_model = models.anthropic.claude_sonnet_4
  agents           = [agents.assistant]

  input "region" {
    type = "string"
    description = "The US Region"
    default = "Midwest"
  }

  task "get_weather" {
    objective = "Get the current weather for the top 5 largest cities in the ${inputs.region} United States"
  }

  task "analyze_weather" {
    objective  = "Based on the weather data collected, determine which city is the coldest, which is the warmest, and calculate the average temperature across all 5 cities"
    depends_on = [tasks.get_weather]
  }
}
