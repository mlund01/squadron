# Custom tool that wraps http.get for weather lookups
tool "weather" {
  implements  = plugins.http.get
  description = "Get the current weather for a city"

  inputs {
    field "city" {
      type        = "string"
      description = "The city to get weather for"
      required    = true
    }
  }

  # Dynamic field from http.get schema, using inputs.city
  url = "https://wttr.in/${inputs.city}?format=3"
}

# Custom tool that wraps http.post for a specific API
tool "create_todo" {
  implements  = plugins.http.post
  description = "Create a new todo item"

  inputs {
    field "title" {
      type        = "string"
      description = "The title of the todo"
      required    = true
    }
    field "priority" {
      type        = "string"
      description = "Priority level (low, medium, high)"
      required    = false
    }
  }

  url  = "https://jsonplaceholder.typicode.com/todos"
  body = {
    title     = inputs.title
    completed = false
    userId    = 1
  }
}
