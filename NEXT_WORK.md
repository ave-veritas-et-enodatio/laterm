# NEXT WORK

@agent coordinator

Plan and execute the process for the following set of changes

## Bugs
1. Some expressions still making it through unchanged
2. Sixel renderings are in red, very hard to read. Should be text color over background color (match terminal)
  - if not possible to detect terminal colors, render as black text on white background 

## Testing Improvement
1. extend integration tests to process test_data/torture_test.md
  - all expressions must be gracefully handled and properly rendered for the test to pass

## Enhancements
1. Add support for iTerm2 color rendering. Use it when terminal is iTerm2 or supports it. Fall back to sixel if possible.
2. Do terminal detection once, early. Do not check iTerm2/sixel repeatedly
3. Allow math expressions to occupy multiple lines if necessary - most likely for display math vs inline.
  - if necessary height can't be determined use heuristic that display math needs 4 text lines worth of height in its target image.
  - if an expression is tall it should top align with current text line
  - consume as many lines as necessary to display it, with blank space to left (in other words just add newlines and have next text line continue below bottom of expression image)
