import 'dart:math';

enum TouchMode { trackpad, direct }

enum MouseButton { left, right }

sealed class PointerCommand {
  const PointerCommand();
}

class PointerMove extends PointerCommand {
  const PointerMove(this.position, {this.dragging = false});
  final Point<double> position;
  final bool dragging;

  @override
  bool operator ==(Object other) =>
      other is PointerMove &&
      position == other.position &&
      dragging == other.dragging;

  @override
  int get hashCode => Object.hash(position, dragging);
}

class PointerButton extends PointerCommand {
  const PointerButton(this.position, this.button, this.down);
  final Point<double> position;
  final MouseButton button;
  final bool down;

  @override
  bool operator ==(Object other) =>
      other is PointerButton &&
      position == other.position &&
      button == other.button &&
      down == other.down;

  @override
  int get hashCode => Object.hash(position, button, down);
}

class PointerWheel extends PointerCommand {
  const PointerWheel(this.delta);
  final Point<double> delta;

  @override
  bool operator ==(Object other) =>
      other is PointerWheel && delta == other.delta;

  @override
  int get hashCode => delta.hashCode;
}

const _zero = Point<double>(0, 0);
const _slop = 7.0;

enum _Motion { undecided, pan, pinch }

/// Screen points are in logical pixels. Pointer positions sent to the host
/// are normalized to the rendered desktop, including its current zoom.
class TouchController {
  TouchController(this.send);

  final void Function(PointerCommand) send;
  final _fingers = <int, Point<double>>{};
  Point<double> _viewport = const Point(1, 1);
  Point<double> _desktop = const Point(1, 1);
  Point<double> _origin = _zero;
  Point<double> _cursor = const Point(.5, .5);
  Point<double> _start = _zero;
  Point<double> _twoCenter = _zero;
  double _twoDistance = 0;
  double _initialZoom = 1;
  double _zoom = 1;
  int _mostFingers = 0;
  bool _moved = false;
  bool _dragging = false;
  bool _held = false;
  _Motion _motion = _Motion.undecided;
  TouchMode _mode = TouchMode.trackpad;

  TouchMode get mode => _mode;
  set mode(TouchMode value) {
    if (_mode == value) return;
    cancel();
    _mode = value;
  }

  Point<double> get origin => _origin;
  Point<double> get cursor => _cursor;
  Point<double> get size => _desktop * _zoom;
  Point<double> get screenCursor =>
      _origin + Point(_cursor.x * size.x, _cursor.y * size.y);
  double get zoom => _zoom;
  bool get canHold =>
      _fingers.length == 1 && _mostFingers == 1 && !_moved && !_held;

  void layout(Point<double> viewport, Point<double> fittedDesktop) {
    if (_viewport == viewport && _desktop == fittedDesktop) return;
    _viewport = viewport;
    _desktop = fittedDesktop;
    _positionAt(Point((viewport.x - size.x) / 2, (viewport.y - size.y) / 2));
    _followCursor();
  }

  void down(int finger, Point<double> point) {
    _fingers[finger] = point;
    _mostFingers = max(_mostFingers, _fingers.length);
    if (_mostFingers == 1) _start = point;
    if (_fingers.length == 2 && _mostFingers == 2) {
      _release();
      _twoCenter = _center;
      _twoDistance = max(1, _distance);
      _initialZoom = _zoom;
    }
  }

  void move(int finger, Point<double> point) {
    final previous = _fingers[finger];
    if (previous == null || previous == point) return;
    final oldCenter = _fingers.length == 2 ? _center : _zero;
    _fingers[finger] = point;
    if (_fingers.length == 2 && _mostFingers == 2) {
      _movePair(oldCenter);
      return;
    }
    if (_mostFingers != 1) return;

    final firstMovement = !_moved;
    if (!_dragging && firstMovement && point.distanceTo(_start) <= _slop) {
      return;
    }
    _moved = true;
    if (mode == TouchMode.direct) return;
    final delta = point - (firstMovement ? _start : previous);
    _cursor = _clampPoint(_cursor + Point(delta.x / size.x, delta.y / size.y));
    _followCursor();
    send(PointerMove(_cursor, dragging: _dragging));
  }

  void hold() {
    if (!canHold) return;
    _held = true;
    if (mode == TouchMode.trackpad) {
      _dragging = true;
      send(PointerButton(_cursor, MouseButton.left, true));
    } else {
      final position = _directPosition(_fingers.values.single);
      if (position != null) _click(position, MouseButton.right);
    }
  }

  void up(int finger) {
    final last = _fingers.remove(finger);
    if (last == null || _fingers.isNotEmpty) return;

    if (!_dragging && !_held && !_moved) {
      if (mode == TouchMode.trackpad && _mostFingers <= 2) {
        _click(
          _cursor,
          _mostFingers == 1 ? MouseButton.left : MouseButton.right,
        );
      } else if (mode == TouchMode.direct && _mostFingers == 1) {
        final position = _directPosition(last);
        if (position != null) _click(position, MouseButton.left);
      }
    }
    cancel();
  }

  void cancel() {
    _release();
    _fingers.clear();
    _mostFingers = 0;
    _moved = false;
    _held = false;
    _motion = _Motion.undecided;
  }

  Point<double> get _center =>
      (_fingers.values.first + _fingers.values.last) * .5;
  double get _distance =>
      _fingers.values.first.distanceTo(_fingers.values.last);

  void _movePair(Point<double> oldCenter) {
    final center = _center;
    final delta = center - oldCenter;
    if (mode == TouchMode.trackpad) {
      if (!_moved && center.distanceTo(_twoCenter) <= _slop) return;
      final travel = center - (_moved ? oldCenter : _twoCenter);
      _moved = true;
      if (travel != _zero) send(PointerWheel(travel));
      return;
    }

    final distance = _distance;
    final ratio = distance / _twoDistance;
    if (_motion == _Motion.undecided) {
      final travel = center.distanceTo(_twoCenter);
      final spread = (distance - _twoDistance).abs();
      if (spread > 10 && spread > travel * 1.6) {
        _motion = _Motion.pinch;
      } else if (travel > _slop && travel * 1.6 >= spread) {
        _motion = _Motion.pan;
      } else {
        return;
      }
    }
    _moved = true;
    if (_motion == _Motion.pinch) {
      final nextZoom = (_initialZoom * ratio).clamp(1.0, 4.0);
      final anchor = (oldCenter - _origin) * (1 / _zoom);
      _zoom = nextZoom;
      _positionAt(center - anchor * _zoom);
    } else if (_zoom > 1 || size.x > _viewport.x || size.y > _viewport.y) {
      _positionAt(_origin + delta);
    } else if (delta != _zero) {
      send(PointerWheel(delta));
    }
  }

  void _followCursor() {
    final visible = screenCursor;
    _positionAt(
      _origin +
          Point(
            _edgeCorrection(visible.x, _viewport.x),
            _edgeCorrection(visible.y, _viewport.y),
          ),
    );
  }

  double _edgeCorrection(double point, double extent) {
    final inset = min(32.0, extent / 4);
    return point.clamp(inset, extent - inset) - point;
  }

  void _positionAt(Point<double> desired) {
    double axis(double point, double desktop, double viewport) =>
        desktop <= viewport
        ? (viewport - desktop) / 2
        : point.clamp(viewport - desktop, 0.0);
    _origin = Point(
      axis(desired.x, size.x, _viewport.x),
      axis(desired.y, size.y, _viewport.y),
    );
  }

  Point<double>? _directPosition(Point<double> point) {
    final local = point - origin;
    if (local.x < 0 || local.y < 0 || local.x > size.x || local.y > size.y) {
      return null;
    }
    return Point(local.x / size.x, local.y / size.y);
  }

  Point<double> _clampPoint(Point<double> value) =>
      Point(value.x.clamp(0.0, 1.0), value.y.clamp(0.0, 1.0));

  void _click(Point<double> position, MouseButton button) {
    _cursor = position;
    send(PointerButton(position, button, true));
    send(PointerButton(position, button, false));
  }

  void _release() {
    if (!_dragging) return;
    _dragging = false;
    send(PointerButton(_cursor, MouseButton.left, false));
  }
}
