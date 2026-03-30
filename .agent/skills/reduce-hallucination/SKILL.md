---
name: Reduce Hallucination
description: Protocols to ensure accuracy, factual correctness, and avoid incorrect assumptions
---
# Reduce Hallucination Protocol

To guarantee the highest quality of assistance and prevent "hallucinated" or fabricated code, APIs, and logic, adhere to these strict rules:

1. **No Assumptions - Verify First**:
   - DO NOT assume the existence of functions, files, variables, schemas, or database tables.
   - ALWAYS use file and text search tools (like `list_dir`, `grep_search`, `view_file`) to inspect the actual codebase before writing any code or architecture referencing these elements.

2. **Wait Before Writing External Libraries**:
   - Never fabricate library names, variable structures, or unproven APIs that don't match the standard or imported modules explicitly visible in the `package.json`, `go.mod`, or imported dependencies.
   - Look up the actual API usage for any newly introduced libraries using standard web search (`search_web`) before writing code relying on them.

3. **Acknowledge Uncertainty**:
   - If an instruction from the user uses an unfamiliar acronym or refers to a concept not obviously visible, clarify rather than guessing.
   - If something isn't found immediately, expand your search or explain what's missing before concluding.

4. **Strict Alignment with Reality**:
   - All proposed code SHOULD compile and run without missing references.
   - If a function takes three parameters, explicitly verify its definition before calling it.
   - Consistently double-check logic across multiple files if an overarching refactor occurs.
