enum ComputerOS {
  mac('macOS 15'),
  windows('Windows 11'),
  linux('Linux'),
  unknown('局域网电脑');

  const ComputerOS(this.label);
  final String label;

  static ComputerOS fromName(Object? name) =>
      values.asNameMap()[name] ?? ComputerOS.unknown;
}
