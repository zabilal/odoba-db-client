# Clean-room policy

**This rule is absolute and applies to every contributor, every session, and every automated agent
working on this repository.**

## The rule

Do not read, copy, adapt, or port source code from [DBGate](https://github.com/dbgate/dbgate).

## Why

DBGate is licensed **GPL-3.0**. Ikigai DB implements a comparable capability set as an independent
work. Ikigai DB's own licence is not yet decided (see `REQUIREMENTS.md` OQ-1). Reading or porting
DBGate source would create a derivative work and force GPL-3.0 onto this project, foreclosing that
decision before it has been made.

## What is permitted

Requirements, feature inventory, and design may be drawn from:

- DBGate's public documentation and website
- Its observable behaviour as a running product
- Public discussion, reviews, and articles

`REQUIREMENTS.md` was written entirely from these sources.

## What is not permitted

- Cloning, browsing, or fetching the DBGate repository to see how something is implemented
- Copying algorithms, data structures, SQL, schemas, or file layouts from its source
- Asking a tool or model to reproduce DBGate source from memory

If you find yourself wanting to check "how does DBGate do this?", the answer is to solve it
independently or consult the database vendor's own documentation.

## Tracking

- `REQUIREMENTS.md` — RISK-5
- `TASKS.md` — standing task S5; licence resolution T4.36
