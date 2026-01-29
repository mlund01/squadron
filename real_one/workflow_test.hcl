# Test workflows for ask_agent functionality
#
# ask_agent supports TWO patterns:
#
# PATTERN 1 - Within a single task:
#   1. Supervisor calls agent with call_agent
#   2. Agent returns a summary
#   3. Supervisor needs more details, calls ask_agent on the SAME agent
#   4. Supervisor completes task with full information
#
# PATTERN 2 - Across dependent tasks (via supervisor inheritance):
#   1. Task 1's supervisor calls an agent, gets a summary
#   2. Task 1 completes
#   3. Task 2 depends on Task 1 - its supervisor INHERITS completed agents
#   4. Task 2's supervisor can use ask_agent to query agents from Task 1

workflow "ask_agent_test" {
  supervisor_model = models.anthropic.claude_sonnet_4
  agents           = [agents.assistant]

  # Single task that demonstrates ask_agent pattern
  # The supervisor needs to:
  # 1. Call agent to research the API (agent provides general summary)
  # 2. Ask follow-up questions to get specific field names for the TypeScript interfaces
  # 3. Generate the final output with precise details
  task "research_and_generate" {
    objective = <<-EOT
      Your task has TWO parts that must be done in sequence:

      PART 1 - RESEARCH:
      First, delegate research to an agent. Tell the agent to:
      - Fetch user data from https://jsonplaceholder.typicode.com/users/1
      - Fetch post data from https://jsonplaceholder.typicode.com/posts/1
      - Provide a HIGH-LEVEL summary of the data structures (general descriptions only)

      PART 2 - GENERATE INTERFACES:
      After receiving the research summary, you need to create TypeScript interfaces
      with EXACT field names. The summary will likely only have general descriptions
      like "user has address info" rather than exact field names.

      You MUST use the ask_agent tool to ask the research agent follow-up questions
      to get the EXACT field names from the API responses. Ask questions like:
      - "What are the exact field names in the user object?"
      - "What are the exact field names in the address sub-object?"
      - "What are the exact field names in the post object?"

      Finally, output complete TypeScript interfaces with accurate field names.
    EOT
  }
}


# Simpler single-task test
workflow "weather_followup_test" {
  supervisor_model = models.anthropic.claude_sonnet_4
  agents           = [agents.assistant]

  task "weather_report" {
    objective = <<-EOT
      Create a detailed weather report for Chicago by following these steps:

      STEP 1: Call an agent to get the current weather for Chicago.
              Tell the agent to describe the weather qualitatively
              (e.g., "cold and cloudy") without specific numbers.

      STEP 2: After receiving the general description, you need SPECIFIC VALUES
              for your report. Use ask_agent to ask the weather agent follow-up
              questions to get the exact temperature and humidity values it observed.

      STEP 3: Generate a formal weather bulletin that includes:
              - The qualitative description from step 1
              - The exact temperature from step 2
              - The exact humidity percentage from step 2
              - Clothing recommendations based on the specific temperature

      You MUST demonstrate using ask_agent to get specific details that weren't
      in the initial summary.
    EOT
  }
}


# Multi-step research task
workflow "book_research_test" {
  supervisor_model = models.anthropic.claude_sonnet_4
  agents           = [agents.assistant]

  task "create_quiz" {
    objective = <<-EOT
      Create a trivia quiz about a famous book by following these steps:

      STEP 1: Call an agent to research "The Great Gatsby" by F. Scott Fitzgerald.
              The agent should use HTTP to find information and provide a
              GENERAL SUMMARY of the plot and themes (no specific character
              names or quotes in the summary).

      STEP 2: To create good quiz questions, you need SPECIFIC DETAILS.
              Use ask_agent to ask follow-up questions like:
              - "What is the main character's full name?"
              - "What is the name of the narrator?"
              - "What is the name of Gatsby's love interest?"
              - "What color is the light at the end of Daisy's dock?"

      STEP 3: Create a 5-question trivia quiz using the specific details
              you gathered from the follow-up questions.

      This tests the ask_agent pattern: general research first,
      then specific follow-ups for details.
    EOT
  }
}


# =============================================================================
# CROSS-TASK ASK_AGENT TEST
# =============================================================================
# This workflow tests that ask_agent works across task boundaries via
# supervisor inheritance. Task 2's supervisor inherits completed agents
# from Task 1 and can query them directly.

workflow "cross_task_ask_agent" {
  supervisor_model = models.anthropic.claude_sonnet_4
  agents           = [agents.assistant]

  # Task 1: Research phase - agent gathers data and returns a summary
  task "gather_data" {
    objective = <<-EOT
      RESEARCH PHASE: Call an agent to fetch and analyze API data.

      Tell the agent to:
      1. Fetch https://jsonplaceholder.typicode.com/users/1
      2. Fetch https://jsonplaceholder.typicode.com/posts?userId=1
      3. Return a HIGH-LEVEL SUMMARY only:
         - "User has personal info and location"
         - "User has N posts on various topics"

      DO NOT include specific field names or values in your final answer.
      Just provide the general overview. The agent will remember the details.

      IMPORTANT: Note the agent_id returned by call_agent - it will be
      inherited by the next task.
    EOT
  }

  # Task 2: Depends on Task 1 - can query the agent from Task 1
  task "generate_report" {
    depends_on = [tasks.gather_data]
    objective  = <<-EOT
      REPORT GENERATION PHASE: Create a detailed report using inherited agent context.

      You are a NEW supervisor, but you have INHERITED completed agents from
      the previous task. The agent that gathered data is available to you.

      Your job:
      1. First, list the available agent_ids you have access to
      2. Use ask_agent to query the research agent from the PREVIOUS task:
         - "What is the user's exact email address?"
         - "What is the user's company name?"
         - "What are the titles of the first 3 posts?"
      3. Generate a formatted report with these specific details

      This demonstrates cross-task agent inheritance - you're querying an
      agent that was created by a DIFFERENT task's supervisor.
    EOT
  }
}
