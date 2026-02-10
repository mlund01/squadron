# Airtable data entry mission
# Uses browser automation to log in and add rows to a table
#
# Browser interaction tips:
# - Use browser_aria_snapshot to understand page structure before interacting
# - If CSS selectors fail/timeout, use browser_click_coordinates as fallback
# - Take screenshots to visually confirm state when needed

mission "airtable_entry" {
  supervisor_model = models.anthropic.claude_sonnet_4
  agents           = [agents.browser_navigator]

  # Dataset of rows to enter into Airtable
  dataset "rows" {
    description = "Rows to add to the Airtable table"
    items = [
      { name = "Project Alpha", notes = "Initial planning phase", status = "todo" },
      { name = "Bug Fix #123", notes = "Fix login timeout issue", status = "in progress" },
      { name = "Documentation", notes = "Update API docs for v2", status = "todo" },
      { name = "Code Review", notes = "Review PR for payments", status = "done" },
      { name = "Deploy Staging", notes = "Deploy latest build", status = "in progress" }
    ]
  }

  task "login" {
    objective = <<-EOT
      Open a browser and log into Airtable.

      1. Go to https://airtable.com/login
      2. Use browser_aria_snapshot to understand the login form structure
      3. Sign in with email "${vars.airtable_email}" and password "${vars.airtable_password}"
      4. Confirm you've reached the dashboard

      Pass along the browser session for subsequent tasks.
    EOT

    output {
      field "session_id" {
        type        = "string"
        description = "The browser session ID to use for subsequent tasks"
        required    = true
      }
      field "logged_in" {
        type        = "boolean"
        description = "Whether login was successful"
        required    = true
      }
    }
  }

  task "navigate_to_table" {
    depends_on = [tasks.login]

    objective = <<-EOT
      Navigate to the correct Airtable base and table.

      1. Use browser_aria_snapshot to find the "maxtest" base on the dashboard
      2. Open the base
      3. Confirm you can see the table with columns: Name, Notes, Status

      Pass along the browser session for the next task.
    EOT

    output {
      field "session_id" {
        type        = "string"
        description = "The browser session ID (passed through from login)"
        required    = true
      }
      field "ready" {
        type        = "boolean"
        description = "Whether table is ready for data entry"
        required    = true
      }
    }
  }

  task "add_row" {
    depends_on = [tasks.navigate_to_table]

    objective = <<-EOT
      Add a new row to the Airtable table with the following data:

      - Name: ${item.name}
      - Notes: ${item.notes}
      - Status: ${item.status}

      Use browser_aria_snapshot to understand the table structure and find the add row button.
      If selectors timeout, use browser_click_coordinates as a fallback.
      The Status field is a dropdown - select the matching option.
      Confirm the row was saved before completing.
    EOT

    iterator {
      dataset  = datasets.rows
      parallel = false
    }

    output {
      field "row_name" {
        type        = "string"
        description = "The name of the row that was added"
        required    = true
      }
      field "success" {
        type        = "boolean"
        description = "Whether the row was added successfully"
        required    = true
      }
    }
  }

  task "verify" {
    depends_on = [tasks.add_row]

    objective = <<-EOT
      Verify that all rows were added to the table.

      Use browser_aria_snapshot to read the table contents and check that these entries exist:
      - "Project Alpha"
      - "Bug Fix #123"
      - "Documentation"
      - "Code Review"
      - "Deploy Staging"

      Close the browser when done and report how many rows were found.
    EOT

    output {
      field "rows_verified" {
        type        = "number"
        description = "Number of rows successfully verified in the table"
        required    = true
      }
      field "all_successful" {
        type        = "boolean"
        description = "Whether all 5 rows were found"
        required    = true
      }
    }
  }
}
