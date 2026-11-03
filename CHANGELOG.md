# Changelog

All notable changes are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

## [1.0.1] - 2026-11-03

### Security

- Imports now skip symbolic links, so erasing an import can't overwrite the file a link points to.

## [1.0.0] - 2026-11-02

### Added

- The first release: `otpauth://` imports, a button unlock code with lockout, the device key, clock correction, and options for the auto-lock time, showing the next code and sorting accounts by name.

[Unreleased]: https://github.com/zakijss/MiniKey/compare/v1.0.1...HEAD
[1.0.1]: https://github.com/zakijss/MiniKey/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/zakijss/MiniKey/releases/tag/v1.0.0
