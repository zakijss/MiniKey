# Changelog

All notable changes are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- X searches the accounts by service or account name, and B lists them all again.
- L2 and R2 jump to the next or previous letter when the accounts are sorted by name, or to the top and the bottom.
- On a code's own screen, UP and DOWN go to the other accounts without going back to the list.

### Changed

- The list's footer shows X Search and L2/R2; SELECT still changes the look, and Options has its row.

## [1.0.2] - 2026-11-08

### Changed

- A failed save says to check that the card has free space.
- An import reports how many accounts it added, not codes.
- Grey and red small text is easier to read (MiniDeck 1.0.3).

## [1.0.1] - 2026-11-03

### Security

- Imports now skip symbolic links, so erasing an import can't overwrite the file a link points to.

## [1.0.0] - 2026-11-02

### Added

- The first release: `otpauth://` imports, a button unlock code with lockout, the device key, clock correction, and options for the auto-lock time, showing the next code and sorting accounts by name.

[Unreleased]: https://github.com/zakijss/MiniKey/compare/v1.0.2...HEAD
[1.0.2]: https://github.com/zakijss/MiniKey/compare/v1.0.1...v1.0.2
[1.0.1]: https://github.com/zakijss/MiniKey/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/zakijss/MiniKey/releases/tag/v1.0.0
