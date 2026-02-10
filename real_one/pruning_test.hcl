mission "pruning_test" {
  supervisor_model = models.anthropic.claude_sonnet_4
  agents           = [agents.browser_navigator]

  task "browse_and_report" {
    objective = <<-EOT
      Navigate to https://news.ycombinator.com and perform the following steps:

      1. Take a screenshot of the homepage
      2. Take an aria snapshot of the homepage
      3. Click on the first story link
      4. Take a screenshot of the story page
      5. Navigate back to the homepage
      6. Take a screenshot of the homepage again
      7. Click on the second story link
      8. Take a screenshot of that story page

      After all steps are complete, review the conversation history.
      Report which tool results are still visible and which show [RESULT PRUNED].
      List each tool call you made and whether its result is still available or was pruned.
    EOT
  }
}
