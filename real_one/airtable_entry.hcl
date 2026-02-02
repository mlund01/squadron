# Airtable data entry workflow
# Uses browser automation to log in and add rows to a table

workflow "airtable_entry" {
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

  # Task 1: Create browser session and log into Airtable
  task "login" {
    objective = <<-EOT
      Open a new browser and log into Airtable.

      Steps:
      1. Navigate to https://airtable.com/login
      2. Use browser_evaluate to find interactive elements:
         document.querySelectorAll('input, button').forEach(el => {
           console.log(el.tagName, el.type, el.placeholder, el.innerText, el.name)
         })
      3. Find the email input (likely input[type="email"] or input[name="email"])
      4. Type email: ${vars.airtable_email}
      5. Find the password input (likely input[type="password"])
      6. Type password: ${vars.airtable_password}
      7. Find and click the submit/sign-in button
      8. Wait for navigation to complete, then use browser_get_text to confirm you see the dashboard

      Tips for understanding the page:
      - Use browser_evaluate with JS to list all buttons: [...document.querySelectorAll('button, [role="button"]')].map(b => b.innerText)
      - Use browser_evaluate to list all inputs: [...document.querySelectorAll('input')].map(i => ({type: i.type, placeholder: i.placeholder, name: i.name}))
      - Use browser_get_text on specific selectors rather than the whole page to avoid noise

      Pass along the browser session info so the next task can continue.
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

  # Task 2: Navigate to the correct base and table
  task "navigate_to_table" {
    depends_on = [tasks.login]

    objective = <<-EOT
      Using the browser session from the login task, navigate to the Airtable table.

      Steps:
      1. From the dashboard, find and click on the base named "maxtest"
         - Use browser_evaluate to find clickable elements containing "maxtest":
           [...document.querySelectorAll('a, button, [role="button"]')].filter(el => el.innerText.includes('maxtest')).map(el => el.innerText)
         - Or use browser_get_text to scan for the base name, then click it
      2. Once in the base, confirm you're on the table view
         - Use browser_get_text to verify you see column headers (Name, Notes, Status)

      Tips for finding elements:
      - Airtable uses data-testid attributes - try: document.querySelectorAll('[data-testid]')
      - Look for table headers: document.querySelectorAll('th, [role="columnheader"]')
      - Check the current URL with browser_evaluate: window.location.href

      Pass along the browser session info for the next task.
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

  # Task 3: Add rows to the table (iterated)
  task "add_row" {
    depends_on = [tasks.navigate_to_table]

    objective = <<-EOT
      Using the browser session from the previous task, add a new row to the Airtable table.

      Data to enter:
      - Name: ${item.name}
      - Notes: ${item.notes}
      - Status: ${item.status}

      Steps:
      1. Find how to add a new row:
         - Look for a "+" button or "Add row" text
         - Use browser_evaluate: [...document.querySelectorAll('button, [role="button"]')].map(b => b.innerText || b.getAttribute('aria-label'))
         - Often the last row in a table is clickable to add new records

      2. Click to add a new record, then find the input cells:
         - Use browser_evaluate to find editable cells:
           document.querySelectorAll('[contenteditable="true"], input, textarea')
         - Airtable cells often have role="textbox" or are contenteditable divs

      3. Fill in each field:
         - For Name: find the first cell/input and type "${item.name}"
         - For Notes: tab or click to the next cell, type "${item.notes}"
         - For Status: this is likely a dropdown/select
           - Click the status cell
           - Use browser_get_text to see dropdown options
           - Click the option matching "${item.status}" (case-insensitive)

      4. Save the row:
         - Press Enter or click outside the row
         - Use browser_wait_for_selector to wait for the save to complete

      Tips for Airtable's UI:
      - Cells are often divs with contenteditable, not traditional inputs
      - Use browser_click on a cell, then browser_type to enter text
      - For dropdowns, click the cell, wait for options, then click the matching option
      - Use browser_evaluate with document.activeElement to see what's focused

      Confirm the row was added by checking the table content.
    EOT

    iterator {
      dataset  = datasets.rows
      parallel = false  # Sequential to avoid conflicts
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

  # Task 4: Verify all rows were added
  task "verify" {
    depends_on = [tasks.add_row]

    objective = <<-EOT
      Using the same browser session, verify that all 5 rows were added to the Airtable table.

      Steps:
      1. Get the table content using browser_evaluate:
         - Extract all row data: [...document.querySelectorAll('[role="row"], tr')].map(row => row.innerText)
         - Or get specific cell values by column

      2. Verify each of these entries exists:
         - "Project Alpha"
         - "Bug Fix #123"
         - "Documentation"
         - "Code Review"
         - "Deploy Staging"

      3. Use browser_get_text on the table area to get a readable summary

      4. Close the browser when done

      Report how many of the 5 rows were successfully found.
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
