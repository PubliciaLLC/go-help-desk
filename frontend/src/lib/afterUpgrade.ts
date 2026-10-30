// The post-upgrade wizard (1.3.0) is for an install that was upgraded, not one
// that was set up on 1.3.0. The server records that without guessing: setup
// writes post_upgrade_130_seen=true on a fresh install, and the wizard writes
// it when it is finished or skipped. An install that upgraded has neither, so
// "not seen" is exactly "has not been asked".
export function needsAfterUpgrade(settings: Record<string, unknown>): boolean {
  return settings.post_upgrade_130_seen !== true
}
