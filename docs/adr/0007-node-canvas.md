# ADR-0007: Node canvas — force-directed layout with culling and level of detail

**Status:** Accepted · **Date:** 2026-09-10
**Spike:** W3 (TASKS.md T0.50–T0.54) · **Gate:** G0-3 **PASSED**

## Context

One canvas serves the ER diagram (FR-8) and the visual query designer (FR-9).
They differ in what a node means, not in how it is laid out, routed or drawn.
The gate was a 200-table schema that "lays out and pans smoothly".

## Decisions

**Force-directed layout, not layered.** Sugiyama-style layered layouts are
excellent for DAGs, and a schema is not one — foreign keys form cycles
routinely, and a layered algorithm must break them arbitrarily, producing a
different picture whenever the schema changes slightly.

**Written directly, not from a library.** `gonum/graph` has no force layout, and
the graph libraries that do are topology-only. The algorithm needs node box
sizes to avoid overlap, which a generic implementation does not model.

**Deterministic.** The same schema always produces the same picture. A diagram
that rearranges itself on every open is not a diagram.

**Components are laid out separately and packed.** Real schemas are one or two
clusters plus a long tail of unreferenced tables — the 200-table fixture has 51
components, one of 150 nodes and 50 singletons. Left to the simulation, isolated
tables are flung to the periphery by repulsion with nothing pulling them back.

**Orthogonal edge routing, attached to port rows.** Many edges run between the
same pair of regions, and straight lines at arbitrary angles become an
unreadable fan. Edges attach to the specific column, because an edge that
points at the box does not say which relationship it is.

## Result

| | Result | Budget |
|---|---|---|
| Layout, 200 tables / 149 edges | **15–28 ms** | 2 s (one-time, on open) |
| Layout, 500 tables | 85 ms | — |
| Pan, overview zoom (183 nodes visible) | **24 µs/frame** | 4 ms |
| Pan, zoom 0.5 | 8 µs/frame | 4 ms |
| Pan, zoom 1.0 | 4 µs/frame | 4 ms |

Two mechanisms carry it, and neither is about drawing faster:

- **Culling.** Only nodes intersecting the viewport become canvas objects.
- **Level of detail.** Below the zoom where column names are legible they are
  not drawn. This matters because the whole-schema overview is precisely the
  view where every node is visible, so culling alone saves nothing there.

Visible node count **saturates at ~51–53 from 50 tables through 800**, against a
geometric viewport capacity of 291 — pan cost depends on the window, not the
schema, which is NFR-P13 for the canvas.

## Bugs and bad tests found

**The layout had no drawing frame.** Fruchterman-Reingold constrains nodes to a
frame; omitting it is not cosmetic. Repulsion is summed over every pair, so
without a bound the equilibrium radius grows as sqrt(n)·k. A 200-table schema
spread across **4862 x 14934** units with empty regions through the middle. With
the frame, and a packing width that follows the largest component instead of a
fixed 2400 the biggest cluster routinely exceeded, the same schema fits a
viewport at zoom 0.15 rather than 0.08.

**Two tests passed for the wrong reason**, and both were more dangerous than the
bug above because they were silent.

`Viewport.Pan` is the graph point at the screen *origin*, not its centre.
Setting `Pan = bounds.Center()` pointed the viewport at empty space, so the
culling test measured **0 of 400 nodes and passed** — zero is not greater than
three times zero. The gate had the same fault compounded: it panned 300 frames
in one direction, drifting off the diagram entirely and timing empty space.
Both now assert they can see something before measuring anything.

**A test premise that was wrong rather than a bug.** After the fix, centring on
the bounds centre still found nothing in a 400-table schema. That one was
legitimate: a schema packs as one large cluster beside rows of isolated tables,
so the centre of the bounding box falls in the gap between them. The test now
centres on a real node.

**An arbitrary threshold hid a geometric truth.** The culling test originally
asserted that an 8x larger schema must not show 3x more nodes. It failed at
15 → 51. That was not a culling failure: at zoom 1.0 a 1440x900 viewport holds
about 52 node slots, so 51 visible means the viewport is simply *full*. The
assertion is now against computed viewport capacity, which is the property that
actually matters and does not need a magic number.

## Follow-ups

- Drawing is not yet implemented; this spike settles layout, culling, routing
  and hit testing, which is where the risk was. Rendering nodes is the same
  problem as grid cells, already answered by ADR-0002.
- Edges whose endpoints are both off-screen are not drawn even if the line
  would cross the viewport. A line arriving from nowhere and leaving to nowhere
  carries no information the user can act on; revisit if it looks wrong in use.
