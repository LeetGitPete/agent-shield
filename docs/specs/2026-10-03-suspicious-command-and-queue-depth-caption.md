# Developer script: a suspicious-probability command; console: a queue depth caption

**Status:** ready-for-agent
Date: 2026-10-03

## Problem Statement

Two things confuse an operator who exercises the pipeline under load.

1. Findings stop at the high load level. The developer script's load command sets the sensor's average rate and its suspicious probability together, and the high level's probability is 0. At about 460 events per second the console's events-in tile is busy while the findings table gets no new rows, which looks like a broken detector. The only way to get suspicious events back is the low level, which also drops the rate to about 10 events per second. There is no way to run a high rate and a small, steady stream of findings at the same time.
2. Queue depth reads as a broken pipeline. The queue depth tile and chart panel show the events queue's ready messages: events published but not yet taken by any detector. Whenever the detectors keep up, which is most of the time at both load levels, the value is 0. Nothing on the page says what the number means, so a 0 next to a busy events-in rate reads as "nothing is happening", and the number is easily mistaken for the number of events.

## Solution

- A new developer-script command, `suspicious P`, sets the probability, from 0 to 1, that a simulated event is suspicious, without touching the rate. The load command keeps setting both values from its level, so the order is: choose the level with `load`, then adjust the probability with `suspicious`. For example `load high` followed by `suspicious 0.0001` gives about 460 events per second and about 3 findings a minute.
- The console's queue depth tile gets a one-line caption under its value: "events waiting for a detector · 0 = keeping up". The chart's queue depth panel shows the same text after its title.

The autoscaler, the load levels and the other tiles do not change.

## User Stories

Suspicious probability command

1. As a platform operator, I want to set the suspicious probability with one command, so that I can get findings at any load level.
2. As a platform operator, I want the command to leave the sensor's rate unchanged, so that adjusting findings does not undo the load I chose.
3. As a platform operator, I want to run the high load level and still see a few findings a minute, so that I can watch detection and autoscaling together.
4. As a platform operator, I want to give the probability as a plain decimal such as 0.0001, so that the value means the same thing as the sensor's own setting.
5. As a platform operator, I want a value outside 0 to 1, a value that is not a number, a missing value or an extra argument refused with a usage line, so that a typo never reaches the sensor.
6. As a platform operator, I want a refused value to leave the sensor untouched and running, so that a typo does not stop the event stream or put the sensor into a crash loop.
7. As a platform operator, I want the command to wait until the sensor runs with the new value, so that when it returns the new probability is in effect.
8. As a platform operator, I want the command to print the resulting load line, the same line the load and status commands print, so that I can confirm the rate and the probability at a glance.
9. As a platform operator, I want the command to work while the load is off, storing the value without starting the sensor, so that switching the load on stays the load command's job.
10. As a platform operator, I want the printed line to say the load is off when it is, so that I do not expect events that will not come.
11. As a platform operator, I want `load low` and `load high` to keep resetting the probability to their level's value, so that each level always means the same thing.
12. As a platform operator, I want the help text and the README to say that the load command resets the probability, so that I know to run the suspicious command after it.
13. As a platform operator, I want status to show "custom" with the full settings once the probability differs from the level's, so that status never claims a level whose values are not in effect.
14. As a platform operator, I want status to show the level's name again when I set the probability back to that level's value, so that returning to a level by hand is recognised.
15. As a platform operator, I want setting the value the sensor already has to change nothing, so that running the command twice is harmless.
16. As a platform operator, I want the command to stop with the usual "stack is down" message when the stack is not deployed, so that it behaves like the other operating commands.
17. As a platform operator, I want the command listed in the script's help and in the README's command table, so that I can find it.
18. As a platform operator, I want a deploy to put the probability back to the low level's value, as it already does for the rate, so that a deploy always starts from a known load.
19. As a maintainer, I want the developer script to stay ASCII only, so that the existing CI check keeps passing and Windows PowerShell reads it correctly.

Queue depth caption

20. As a console reader, I want a short caption under the queue depth value, so that I know what the number counts without asking.
21. As a console reader, I want the caption to say that 0 means the detectors are keeping up, so that a 0 next to a busy event rate does not look like a broken pipeline.
22. As a console reader, I want the same explanation beside the queue depth chart panel's title, so that the chart is just as clear as the tile.
23. As a console reader, I want the caption in small, muted, normal-case type, so that it reads as an explanation and not as another label.
24. As a console reader, I want the caption visible before the first statistics arrive, so that the tile explains itself from the moment the page opens.
25. As a console reader, I want the caption to dim with the rest of the tile while statistics are unavailable, so that the tile keeps behaving as one unit.
26. As a console reader, I want the four tiles to stay the same height, so that the strip stays aligned with a caption on only one of them.
27. As a console reader on a narrow screen, I want the caption and the chart hint to wrap rather than overflow, so that they never clip or force horizontal scrolling.
28. As a console reader using a screen reader, I want the caption announced as the tile's description while its name stays "queue depth", so that the explanation is available without changing how the tile is found.
29. As a console reader, I want the other three tiles unchanged, so that the strip does not grow more text than it needs.

## Implementation Decisions

### Developer script: the suspicious command

- New command `suspicious P`, listed in the help text beside the load command. It takes exactly one argument.
- The argument is checked before anything touches the cluster. It must parse as a number in the invariant culture and lie from 0 to 1 inclusive; NaN and infinities fail the range check. Any other input, a missing argument or an extra argument stops the script with the usage line, which names the range and gives an example (0.0001).
- The script passes on the value in its own invariant-culture formatting, not as typed. Whatever the script accepts, the sensor's parser accepts too, and a value equal to a level's (0.005, 0) matches that level's stored text, so status names the level again.
- Like load, triage, clean and simulate-attack, the command first checks that the stack is deployed and otherwise stops with the "stack is down" message.
- It sets only the sensor Deployment's SENSOR_SUSPICIOUS_PROB, waits for the sensor's rollout and prints the load line built by the existing load description. It never changes the sensor's replica count: with the load off the value is stored, the sensor stays at zero replicas and the load line says "off".
- Setting the value the Deployment already has changes nothing, so no restart happens.
- The load levels and the load command are unchanged: `load low` and `load high` still set the rate and the probability together, high's probability stays 0.
- The load description is unchanged: it names a level only when both the rate and the probability match it, and says "custom" with the full settings otherwise.
- A deploy re-applies the sensor manifest, which holds the low level, so it resets the probability as it resets the rate. This is existing behaviour and needs no change.
- The script stays ASCII only.

### Console: queue depth caption

- Caption text, identical in both places: "events waiting for a detector · 0 = keeping up".
- Tile: the tile component gains an optional caption, shown as a line under the value in 11px muted, normal-case type. Only the queue depth tile passes one. The caption sits inside the tile, so it shows before the first statistics arrive and dims with the tile while statistics are unavailable.
- The caption is the tile's accessible description. The tile's accessible name stays its label, so the tile is still found as "queue depth".
- The strip's grid already stretches the tiles in a row to equal height, and gives its rows equal height when the tiles take two rows; the other tiles need no placeholder.
- Chart: the panel component gains an optional hint, shown inline after the panel title, separated by a space, in the same muted colour and normal case rather than the title's uppercase. Only the queue depth panel passes one. On narrow screens the hint wraps under the title.
- No API change: the tile still shows the ready count from the statistics endpoint.

### Documentation

- The script's help text gains the suspicious command, and the load command's line says that a level also resets the probability.
- The README's command table gains a row for the suspicious command, with the high-level example, and its load row says that each level resets the probability.
- The detector scaling and console spec (2026-10-02) is brought in line: its developer-script command list gains the suspicious command and the list of commands that first check the stack is deployed includes it; its console description of the top strip mentions the queue depth caption and the chart hint.
- The architecture diagram and its notes stay as they are: they describe the low level and the chart, which do not change.

### Delivery

- Two commits, one per change: the suspicious command with its documentation, then the queue depth caption with its tests and documentation.

## Testing Decisions

- A good test checks behaviour a user can observe, the rendered page or the script's effect on the cluster, not component props or internal functions.
- Console: one seam, the existing one. The console's test suite renders the whole application against a stubbed fetch; everything from the request to the DOM is real. New assertions there:
  - the queue depth tile contains the caption text, and the caption is the tile's accessible description, while the tile is still found by the name "queue depth";
  - the other three tiles have no caption;
  - the pipeline history figure shows the hint beside the queue depth panel title.
  - Prior art: the tile tests that find tiles by role and name and check their values and dimming, and the chart tests that read the "samples held" caption.
- Existing console tests must keep passing unchanged; the tiles' names do not change.
- Developer script: no automated tests, as for its other commands. Manual check on the local cluster:
  - `suspicious abc`, `suspicious -1`, `suspicious 2`, `suspicious` and `suspicious 0.1 0.2` are refused with the usage line and leave the sensor running with its old value;
  - after `load high`, `suspicious 0.0001` prints a custom load line with probability 0.0001, the sensor log reports a few suspicious events per interval and findings appear in the console;
  - `load high` afterwards prints the high level again with probability 0;
  - after `load low`, `suspicious 0.005` prints the low level;
  - after `load off`, `suspicious 0.01` prints an off line with the new value, and no sensor pod starts;
  - with the stack down the command prints the "stack is down" message;
  - the CI check that the script is ASCII only passes.

## Out of Scope

- The autoscaler: the scale-down stabilization window stays 60 seconds and scale-up keeps its defaults.
- The load levels' own values, including the high level's probability of 0.
- Making the load command leave the probability alone, or a flag on the load command to set it.
- Marking an overridden probability in the load description beyond the existing "custom".
- Captions on the detectors, events-in and events-out tiles.
- An equivalent command under compose, where the sensor's settings stay in the compose file.
- Changes to the sensor, the detector, the API or the architecture diagram.

## Further Notes

- At the high level's average of about 460 events per second, the probability gives roughly 460 × P × 60 findings a minute: 0.0001 gives about 3, the low level's rate of findings; 0.001 gives about 28. Each finding is also a triage request, so with the Gemini provider a high probability spends provider quota at the same pace.
- Changing the probability restarts the sensor (the Deployment uses the recreate strategy), so the event stream pauses for a few seconds and the new sensor starts a fresh random load pattern. The 60-second scale-down window keeps the detector count through the pause.
