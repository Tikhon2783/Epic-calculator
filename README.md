Здесь будет описание работы "Распределенного вычислителя арифметических выражений" :)

REMEMBER TO:
<!-- - Uncomment startup -->
<!-- - Redirect logger -->
- Put contacts
- Clear + explain vars/variables.go
- Delete server self destruction function
- Translate output text
- Remove unfunny ahh comments :\
    - Agent:102
    - Agent: break Free cycle

DON'T FORGET TO:
<!-- - Close task giver channel in orchestrator when agent dies -->

TODO:
- Orchestrator server
    - Middlewares
        - RequestId check
        - Valid expression check
    - Post requests
        - Time vars changes (db only)
        - Actual expressions
            - Parsing
            - Organizing agent(s)
    - Check for tasks at start
- Agents
    - Check for continuing instead of starting new
    <!-- - Calculators-goroutines -->
<!-- - Frontend
    - Html pages -->
- Requests script
    - Complete shutdown
    - Sending GET/POST commands to orchestrator
    - Ordhestrator/Agent kills
<!-- - Decide way of creating tables (system fails handler), either:
    - Check for continuing in main - execute/ignore startup
    - Leave as is but ask to use shutdown file -->
