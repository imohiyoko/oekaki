/* The viewer's canvas is maxGraph. This file is the list of what it uses.
 *
 * A namespace import costs 623 KB because nothing can be dropped; naming the
 * symbols brings the bundle to under 400 KB. Adding a symbol here and running
 * `make vendor-maxgraph` is the way to reach for more of the library — the
 * bundle beside this file is generated from it and checked in, so a clone
 * builds and renders with no npm at all. */
import {
  Graph,
  InternalEvent,
  Point,
  RectangleShape,
  RubberBandHandler,
  ShapeRegistry,
} from '@maxgraph/core';

window.maxGraph = {
  Graph, InternalEvent, Point, RectangleShape, RubberBandHandler, ShapeRegistry,
};
