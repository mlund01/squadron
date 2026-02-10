mission "midwest_weather" {
  supervisor_model = models.anthropic.claude_sonnet_4
  agents           = [agents.assistant]


  dataset "city_list" {
    description = "Midwest cities to process"
    schema {
      field "name" {
        type     = "string"
        required = true
      }
      field "state" {
        type = "string"
      }
    }
  }

  task "get_cities" {
    objective = "pull the midwest cites form the data.json file and update the appropriate dataset with the list data"
  }

  task "get_weather" {
    objective = "Get the current weather for ${item.name}, ${item.state}. Include temperature, conditions, and humidity."

    iterator {
      dataset     = datasets.city_list
      parallel    = false
      max_retries = 2
    }
    depends_on = [tasks.get_cities]

    output {
      field "temperature" {
        type        = "number"
        description = "Current temperature in Fahrenheit"
        required    = true
      }
      field "conditions" {
        type        = "string"
        description = "Weather conditions (e.g., sunny, cloudy, rainy)"
        required    = true
      }
      field "humidity" {
        type        = "number"
        description = "Humidity percentage"
      }
    }
  }

  task "analyze_weather" {
    objective  = "Based on the weather data collected for all cities, determine which city is the coldest, which is the warmest, and calculate the average temperature across all cities"
    depends_on = [tasks.get_weather]

    output {
      field "coldest_city" {
        type        = "string"
        description = "Name of the coldest city"
        required    = true
      }
      field "warmest_city" {
        type        = "string"
        description = "Name of the warmest city"
        required    = true
      }
      field "avg_temperature" {
        type        = "number"
        description = "Average temperature across all cities"
        required    = true
      }
    }
  }
}
