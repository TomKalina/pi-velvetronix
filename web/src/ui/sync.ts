// Decides when to read the amp state (readAll) and when to drop it (reset). No DOM.

import type { AmpState } from "../protocol/client";
import { ampErrorText } from "./errors";

export interface SyncClient {
  readAll(): Promise<AmpState>;
  reset(): void;
}

export class AmpSync {
  ready = false;
  reading = false;
  error: string | null = null;
  private live = false;
  /** Bumped on every live change so a stale readAll() result is ignored. */
  private gen = 0;

  constructor(
    private readonly client: SyncClient,
    private readonly onChange: () => void,
  ) {}

  /**
   * live = WebSocket open AND amp "connected" AND no update in progress. Reads only, never writes.
   * keep = watching another controller: the model stays and follows its traffic, it is re-read once live.
   */
  update(live: boolean, keep = false): void {
    if (live === this.live) return;
    this.live = live;
    this.gen++;
    if (live) {
      this.read();
      return;
    }
    if (keep) {
      this.reading = false;
      this.onChange();
      return;
    }
    this.client.reset();
    this.ready = false;
    this.reading = false;
    this.error = null;
    this.onChange();
  }

  /** Updates were missed: read again, even while a read runs (its earlier replies may be stale). */
  resync(): void {
    if (!this.live) return;
    this.gen++;
    this.read();
  }

  retry(): void {
    if (!this.live || this.reading) return;
    this.gen++;
    this.read();
  }

  private read(): void {
    const gen = this.gen;
    this.ready = false;
    this.reading = true;
    this.error = null;
    this.onChange();
    this.client.readAll().then(
      () => {
        if (gen !== this.gen) return;
        this.reading = false;
        this.ready = true;
        this.onChange();
      },
      (e: unknown) => {
        if (gen !== this.gen) return;
        this.reading = false;
        this.error = ampErrorText(e);
        this.onChange();
      },
    );
  }
}
