model "openai" {
  provider       = "openai"
  allowed_models = ["gpt_4o", "gpt_4o_mini", "gpt_4_turbo"]
  api_key        = vars.openai_api_key
}

model "gemini" {
  provider       = "gemini"
  allowed_models = ["gemini_2_0_flash", "gemini_1_5_pro", "gemini_1_5_flash"]
  api_key        = vars.gemini_api_key
}

model "anthropic" {
  provider       = "anthropic"
  allowed_models = ["claude_sonnet_4", "claude_3_5_haiku", "claude_opus_4"]
  api_key        = vars.anthropic_api_key
}
