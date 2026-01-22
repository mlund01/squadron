variable "openai_api_key" {
  secret = true
}

variable "anthropic_api_key" {
  secret = true
}

variable "gemini_api_key" {
  secret = true
}

variable "app_name" {
  default = "squad"
}
