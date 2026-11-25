import 'dart:math';

import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/touch_controller.dart';

const p = Point<double>.new;
const viewport = Point<double>(400, 200);

void main() {
  late List<PointerCommand> events;
  late TouchController touch;

  setUp(() {
    events = [];
    touch = TouchController(events.add)..layout(viewport, viewport);
  });

  test(
    'trackpad moves the remote cursor relatively and taps at the cursor',
    () {
      touch.down(1, p(30, 30));
      touch.move(1, p(70, 50));
      touch.up(1);
      expect(events.single, PointerMove(p(.6, .6)));
      touch.down(1, p(12, 16));
      touch.up(1);
      expect(events.skip(1), [
        PointerButton(p(.6, .6), MouseButton.left, true),
        PointerButton(p(.6, .6), MouseButton.left, false),
      ]);
    },
  );

  test('trackpad two finger tap right clicks and two finger slide scrolls', () {
    touch.down(1, p(30, 30));
    touch.down(2, p(90, 30));
    touch.up(1);
    touch.up(2);
    expect(events, [
      PointerButton(p(.5, .5), MouseButton.right, true),
      PointerButton(p(.5, .5), MouseButton.right, false),
    ]);
    events.clear();
    touch.down(1, p(30, 30));
    touch.down(2, p(90, 30));
    touch.move(1, p(30, 50));
    touch.move(2, p(90, 50));
    touch.up(1);
    touch.up(2);
    expect(events, [PointerWheel(p(0, 10)), PointerWheel(p(0, 10))]);
  });

  test('trackpad hold drags and cancellation releases the button', () {
    touch.down(1, p(30, 30));
    touch.hold();
    touch.move(1, p(70, 50));
    touch.cancel();
    expect(events, [
      PointerButton(p(.5, .5), MouseButton.left, true),
      PointerMove(p(.6, .6), dragging: true),
      PointerButton(p(.6, .6), MouseButton.left, false),
    ]);
  });

  test('trackpad follows the cursor near the edge of a cropped desktop', () {
    touch.layout(viewport, p(600, 200));
    touch.down(1, p(100, 100));
    touch.move(1, p(295, 100));
    expect(touch.origin.x, lessThan(-100));
    expect(touch.cursor.x, closeTo(.825, .00001));
  });

  test('direct tap and long press click their exact locations', () {
    touch.mode = TouchMode.direct;
    touch.down(1, p(100, 150));
    touch.up(1);
    touch.down(1, p(300, 50));
    touch.hold();
    touch.up(1);
    expect(events, [
      PointerButton(p(.25, .75), MouseButton.left, true),
      PointerButton(p(.25, .75), MouseButton.left, false),
      PointerButton(p(.75, .25), MouseButton.right, true),
      PointerButton(p(.75, .25), MouseButton.right, false),
    ]);
  });

  test('direct quick movement does not accidentally click', () {
    touch.mode = TouchMode.direct;
    touch.down(1, p(100, 100));
    touch.move(1, p(125, 100));
    touch.up(1);
    expect(events, isEmpty);
  });

  test('direct pinch zooms and a two finger slide pans when zoomed', () {
    touch.mode = TouchMode.direct;
    touch.down(1, p(140, 100));
    touch.down(2, p(260, 100));
    touch.move(1, p(80, 100));
    touch.move(2, p(320, 100));
    expect(touch.zoom, closeTo(2, .001));
    final origin = touch.origin;
    touch.up(1);
    touch.up(2);
    touch.down(1, p(100, 100));
    touch.down(2, p(300, 100));
    touch.move(1, p(100, 125));
    touch.move(2, p(300, 125));
    expect(touch.origin.y, greaterThan(origin.y));
    expect(touch.zoom, closeTo(2, .02));
    expect(events, isEmpty);
  });

  test('direct two finger slide scrolls at fit size', () {
    touch.mode = TouchMode.direct;
    touch.down(1, p(100, 100));
    touch.down(2, p(300, 100));
    touch.move(1, p(100, 130));
    touch.move(2, p(300, 130));
    touch.up(1);
    touch.up(2);
    expect(events, [PointerWheel(p(0, 15)), PointerWheel(p(0, 15))]);
  });

  test('direct pinch in does not turn into scrolling at fit size', () {
    touch.mode = TouchMode.direct;
    touch.down(1, p(100, 100));
    touch.down(2, p(300, 100));
    touch.move(1, p(130, 100));
    touch.move(2, p(270, 100));
    touch.up(1);
    touch.up(2);
    expect(events, isEmpty);
    expect(touch.zoom, 1);
  });

  test('direct touches on letterboxing never send a click', () {
    touch.mode = TouchMode.direct;
    touch.layout(viewport, p(300, 200));
    touch.down(1, p(30, 100));
    touch.up(1);
    expect(events, isEmpty);
  });
}
