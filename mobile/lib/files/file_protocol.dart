import 'dart:convert';
import 'dart:typed_data';

const fileProtocolVersion = 1;
const fileChunkSize = 63 << 10;
const fileMaxText = 60 << 10;
const fileMaxJson = 32 << 20;
const fileHighWater = 4 << 20;
const fileLowWater = 1 << 20;
const fileDataKind = 1;
const fileJsonKind = 2;
const _headerSize = 17;

typedef FileMessage = Map<String, dynamic>;

String encodeFileMessage(FileMessage message) =>
    jsonEncode(message)
        .replaceAll('&', r'\u0026')
        .replaceAll('<', r'\u003c')
        .replaceAll('>', r'\u003e')
        .replaceAll('\u2028', r'\u2028')
        .replaceAll('\u2029', r'\u2029');

FileMessage decodeFileMessage(List<int> bytes) {
  final value = jsonDecode(utf8.decode(bytes));
  if (value is! FileMessage) {
    throw const FormatException('Expected a JSON object');
  }
  return value;
}

class FileFrame {
  const FileFrame(this.kind, this.transfer, this.offset, this.payload);

  final int kind;
  final int transfer;
  final int offset;
  final List<int> payload;

  factory FileFrame.decode(Uint8List bytes) {
    if (bytes.length < _headerSize || bytes.length > 65535) {
      throw const FormatException('Invalid files frame length');
    }
    final header = ByteData.sublistView(bytes);
    return FileFrame(
      header.getUint8(0),
      header.getUint64(1),
      header.getInt64(9),
      Uint8List.sublistView(bytes, _headerSize),
    );
  }

  Uint8List encode() {
    final bytes = Uint8List(_headerSize + payload.length);
    ByteData.sublistView(bytes)
      ..setUint8(0, kind)
      ..setUint64(1, transfer)
      ..setInt64(9, offset);
    bytes.setRange(_headerSize, bytes.length, payload);
    return bytes;
  }
}

class FileChannelMessage {
  const FileChannelMessage.text(String this.text) : binary = null;
  const FileChannelMessage.binary(Uint8List this.binary) : text = null;

  final String? text;
  final Uint8List? binary;
}

/// An ordered, reliable, message-based channel. End the incoming stream when
/// the connection closes. The caller owns the physical connection.
abstract interface class FileChannel {
  Stream<FileChannelMessage> get messages;
  Stream<void> get bufferedAmountLow;
  set bufferedAmountLowThreshold(int bytes);
  Future<int> bufferedAmount();
  Future<void> sendText(String text);
  Future<void> sendBinary(Uint8List bytes);
}
