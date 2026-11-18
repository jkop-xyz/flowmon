# Write our own IPFIX/NetFlow v9 decoder instead of using goflow2

Flowmon has to be fully compatible with IPFIX (RFC 7011 + RFC 6313) and not just with MikroTik. No maintained Go library covers that, so we write and own the decoder. goflow2 is the closest match, but it only receives over UDP, keys templates by router address rather than by transport session, returns RFC 6313 lists as raw bytes, and allocates from unvalidated lengths. Closing those gaps would mean rewriting its core while still carrying its data model and its pending v3 API break. Two independent reviews both chose this option, with 65–75% confidence.

## Consequences

- Records use a lossless value model from day one: (enterprise number, IE ID, raw bytes) plus typed values decoded only when needed. The model is recursive, so RFC 6313 lists fit later without a storage migration.
- The parser treats every packet as hostile. Every read is bounds-checked, nothing is allocated from an unvalidated length, each packet runs under `recover`, and there are caps on templates, fields, nesting depth and Exporters.
- Templates are scoped by (transport session, Observation Domain, Template ID). Over UDP they expire and withdrawals are ignored (RFC 7011 §8.4). Over TCP they are withdrawn explicitly or die with the session.
- The IE registry is generated from a pinned copy of IANA's CSV, checked into the repo.
- Correctness depends on the tests, not on the library's production record: fuzzing, round-trips through a test-only encoder, and differential tests against goflow2 and tshark on a pcap corpus that includes MikroTik and YAF traffic.
- The goal is to be **correct wherever we implement something, and to work with common exporters**. Full RFC 7011 compliance is the long-term aim, and it includes SCTP, which RFC 7011 makes mandatory.
- v1 covers:
  - IPFIX over UDP
  - NetFlow v9 and v5
  - options templates
  - sampling: counts are scaled up and marked as estimates
  - RFC 5103 biflows
  - both total and delta counters
- After v1: TCP, then RFC 6313 decoding, then SCTP.
- Work starts with a thin end-to-end slice: MikroTik over UDP, into the Flow Store, onto one Overview page.
- **We build to the standards, not to observed device behaviour.** If a vendor documents support for IPFIX or NetFlow v9, we take that at face value. Things the standard allows a device to choose (4- or 8-byte counters, which timestamp fields it sends, which IEs it includes) are handled by the decoder in general, never assumed per vendor. Device-specific workarounds are added only after a real problem shows up in real traffic. There is no up-front capture-and-verify phase. Instead, the collector has a debug option (`FLOWMON_DEBUG_CAPTURE`) that saves the datagrams it receives as a pcap, and the Exporters page shows each received Template's fields and sizes. Real captures then come from normal use and become test data.
- **Lenient decoding, strict reporting.** The decoder rejects data only when decoding it would be unsafe. Every deviation from the standard is counted by type and shown per Exporter, with a "0% decoded" banner when nothing from an Exporter gets through. The Exporter page's truth panel shows each Template's fields and sizes, actively flags fields Flowmon needs that are missing or too short (for example 4-byte byte counters, or no interface numbers), shows how interface numbers map to names next to live Flow counts, and compares Flow times with arrival times. Sequence-number gaps are shown as a counter, never as an alarm, in v1.
- **Debug capture** is off by default, limited by size and run time, rotated, excluded from snapshots, and anonymised before a capture becomes a shared test fixture, because it holds private metadata about who talked to whom.
