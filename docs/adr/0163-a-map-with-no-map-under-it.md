# ADR-0163: A map with no map under it

**Status:** Accepted · **Date:** 2026-09-25
**Tasks:** T5.11 · **Requirements:** FR-11.5, FR-11.4, NFR-D6
**Packages:** `internal/model`, `internal/ui/geomap`, `internal/ui/shell`

## Context

FR-11.5 asks for a map view for geo columns — PostGIS geometry, lat/lon pairs,
GeoJSON — and the requirements say of it, in the build note beside it, that there
is "no viable pure-Go tile-map story".

That is the whole difficulty. A map, to most people, means a street map: tiles
drawn by somebody else, fetched from their server as you pan. This application
does nothing on the network of its own accord (NFR-D6), and a basemap would put a
stranger's host in the path of somebody looking at their own data — every pan
telling that host which part of the world is being looked at.

## Decisions

1. **There is no basemap, and the map does not pretend to be one.** What is drawn
   is the geometry itself on a graticule of degrees. That answers the questions a
   map over a result is actually asked — did the polygons come out right, where
   are these points in relation to each other, which row is this one — and it
   answers none of the questions a street map answers. The footer says what the
   places were drawn from; nothing says "map of the world", because it is not one.

2. **Longitude across, latitude up, and nothing about the curve of the Earth.**
   Right for a county, wrong for a hemisphere, and said where it is drawn rather
   than hidden. A projection that was right everywhere would be a projection
   library, which is a dependency and a decision for a day when somebody needs it.

3. **The two axes are to the same scale.** A degree across and a degree up are the
   same length on the screen, so the wider axis decides and the other gets the
   slack. A square lake drawn as a rectangle is a picture that misleads about the
   data, which is the one thing a picture of data may not do.

4. **A geometry is read twice, and by the same reader.** `model.Geometry` already
   wrote well-known binary as text for a cell; `model.Coordinates` reads the same
   bytes as positions, sharing the header and primitive reading so that the two
   cannot disagree about what is in the bytes. GeoJSON is read beside it, with RFC
   7946's order — longitude first — taken as read and never guessed at, because
   nothing in the numbers can tell you which way round a file meant them.

5. **Polygons are outlines, not fills.** A fill needs a rule about which way a
   hole goes, and a rule applied wrongly to somebody's data draws a lake where
   there is land. An outline cannot be wrong that way. The rings are read
   faithfully, so a fill later is a drawing change and not a reading one.

6. **A pair of columns is found by name and never by position.** Two numeric
   columns side by side are not coordinates. Drawing them as though they were
   would put somebody's prices in the Atlantic, and it would look plausible.

7. **A geometry column wins over a pair, and a type wins over a value.** A column
   whose type is a geometry is not a guess at all; a JSON column is a guess about
   its values, so its values are asked; text that reads as well-known binary is
   the last resort, because that is how SQLite and MySQL hand a geometry to a
   driver that was not told what the column is.

8. **Every guess can be changed,** because it will be wrong sometimes: the
   toolbar offers the geometry column or the pair, and choosing one puts the other
   down — they are two answers to the same question.

9. **The pointer says where it is, in bearings.** 51.5000°N, not 51.5: a
   coordinate is read aloud as a bearing, and a number alone could be anything. A
   click goes to the row, which is the point of drawing a result at all (FR-11.4).

10. **Twenty thousand rows, not a hundred thousand.** A chart's marks are one
    raster; a map's are shapes, and a hundred thousand polygons would be a
    hundred thousand lines in the scene. How many were read is always said.

11. **One guard where there were two.** The frame expanded a degenerate span on
    each axis separately, and neither expansion could be told from the other:
    whichever axis has a span gives the other one its own, through the
    equal-scale calculation, so only a rectangle with no size in *either*
    direction needs anything done to it. Found by taking each guard away and
    asking which test noticed; neither did.

## Consequences

The map is drawn from the same scene description as every other picture here, so
it is one drawing shown one way (ADR-0130) — but there is no export yet: the
chart's PNG and SVG go through its own export path, and the map has none. That is
the next thing anybody will ask for.

What is not here, and would each be a decision of its own: a basemap of any kind,
including an offline one shipped with the application (a simplified coastline is a
few megabytes and a licence to read); panning and zooming, which without tiles
would be a scale change and a redraw and is worth doing; filled polygons; a
projection that holds at continental scale; and clustering, which is what a map of
a million points needs to say anything at all.

## Alternatives

**Tiles from OpenStreetMap or anybody else.** Rejected by NFR-D6 and by what it
tells the tile server about what somebody is looking at. A person who wants that
can paste coordinates into a browser, which at least makes the request theirs.

**An offline basemap shipped with the binary.** Not rejected, deferred: the binary
is already over its budget (T4.30), a coastline good enough to be useful is
megabytes, and every dataset has a licence to honour. Worth revisiting if the
size decision goes the way of a full build and a core one.

**Drawing nothing until somebody chooses the columns.** Rejected: a map that
opened empty and waited would be a map nobody used. It guesses, says what it
guessed, and lets it be changed.

**A geometry column read as text and parsed with a regular expression.** Rejected:
there is a reader already, and a second one would be a second answer about the
same bytes.
