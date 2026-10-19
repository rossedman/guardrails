---
name: status
description: Show what Guardrails is doing in this repository — which languages it gates and to what bar, the last gate result, corrections waiting to become rules — and the one thing to do next. Use when the user asks what Guardrails is doing, whether it's on, why Claude was blocked, or what to do next.
---

# Show Guardrails' status

1. Run:

   ```bash
   guardrails status
   ```

   (`guardrails status --json` gives the same facts as data.)

2. Explain it to the user in plain words, in a few short lines:
   - **Is it on, and for what?** The languages it gates here and the bar
     for each, e.g. "Go and Shell, strict preset: 90% coverage and 90% of
     the lines Claude changes must be tested."
   - **Where the config comes from:** the preset, and whether a
     `.guardrails.yaml` or the user-wide config changes it.
   - **The last gate:** passed or failed, coverage against the threshold,
     and lint issues. If it failed, say which check in one line.
   - **Anything waiting:** corrections recorded as lessons, tools still
     installing, a config error.

   Skip what's unremarkable. Don't paste the raw output unless asked.

3. End with the `Next:` line from the output, phrased as an offer, e.g.
   "Want me to run /guardrails:learn to turn your 3 corrections into
   rules?" Do it only if the user agrees.

If status shows Guardrails isn't set up for this repo yet, offer
/guardrails:setup. If the user wants the `guardrails` command in their own
terminal, relay the `ln -s` line from the output; don't run it for them.
