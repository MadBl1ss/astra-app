<script lang="ts">
  import Button from '../components/Button.svelte';
  import Dialog from '../components/Dialog.svelte';
  import Icon from '../components/Icon.svelte';
  import { bytes, dayMonth } from '../format';
  import { ru } from '../i18n/ru';
  import { updates } from '../update.svelte';
  import { app } from '../version';

  // The offer of a new version, its download and the start after it, in one
  // dialog: each step replaces the last in place, so the user never wonders
  // which window to look at. Mounted by App while updates.open.
  const t = ru.update;
  const u = updates;

  const info = $derived(u.available);
  const title = $derived.by(() => {
    if (u.phase === 'done') return t.doneTitle(u.installed ?? app.version);
    if (u.phase === 'missed') return t.missedTitle;
    return info ? t.title(info.version) : t.missedTitle;
  });

  // The notes of a release are a few plain lines; "- " starts a list item.
  const notes = $derived.by(() => {
    const lines = (info?.notes ?? '')
      .split(/\r?\n/)
      .map((l) => l.trim())
      .filter(Boolean);
    return lines.map((l) => ({ item: /^[-*•]\s+/.test(l), text: l.replace(/^[-*•]\s+/, '') }));
  });

  const share = $derived(u.total ? Math.min(1, u.got / u.total) : null);
  const working = $derived(u.phase === 'download' || u.phase === 'verify' || u.phase === 'install');
</script>

<Dialog {title} width={480} onclose={() => u.close()}>
  {#snippet children()}
    {#if u.phase === 'done'}
      <p>{t.doneBody}</p>
    {:else if u.phase === 'missed'}
      <p>{t.missedBody(u.installed ?? '', app.version)}</p>
    {:else if info}
      <p class="sub">
        {info.date ? t.released(dayMonth(info.date, Date.now()), app.version) : t.current(app.version)}
      </p>

      {#if u.phase === 'offer' || u.phase === 'error'}
        <div class="notes" tabindex="-1">
          {#if notes.length === 0}
            <p class="muted">{t.notesEmpty}</p>
          {:else}
            {#each notes as n, i (i)}
              <p class:item={n.item}>{n.text}</p>
            {/each}
          {/if}
        </div>
      {/if}

      {#if u.phase === 'offer'}
        <p class="warn"><Icon name="alert" size={15} /><span>{t.warning}</span></p>
      {:else if u.phase === 'error'}
        <p class="error" role="alert"><Icon name="alert" size={15} /><span>{u.error}</span></p>
      {:else}
        <div class="progress" role="status">
          <div class="line">
            <span>
              {u.phase === 'download' ? t.downloading : u.phase === 'verify' ? t.verifying : t.restarting}
            </span>
            {#if u.phase === 'download'}
              <span class="num">{t.progress(bytes(u.got), u.total ? bytes(u.total) : null)}</span>
            {/if}
          </div>
          <div
            class="bar"
            class:loose={share === null && u.phase === 'download'}
            role="progressbar"
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={u.phase === 'download' && share !== null ? Math.round(share * 100) : undefined}
          >
            <span style:width="{u.phase === 'download' ? (share ?? 0.3) * 100 : 100}%"></span>
          </div>
          {#if u.phase === 'install'}<p class="muted">{t.restartingNote}</p>{/if}
        </div>
      {/if}
    {/if}
  {/snippet}
  {#snippet actions()}
    {#if u.phase === 'done'}
      <Button onclick={() => u.close()}>{t.ok}</Button>
    {:else if u.phase === 'missed'}
      <Button variant="ghost" onclick={() => u.close()}>{t.later}</Button>
      {#if info}<Button icon="download" onclick={() => u.install()}>{t.retry}</Button>{/if}
    {:else if u.phase === 'error'}
      <Button variant="ghost" onclick={() => u.close()}>{t.close}</Button>
      <Button icon="refresh" onclick={() => u.install()}>{t.retry}</Button>
    {:else if u.phase === 'offer'}
      <Button variant="ghost" onclick={() => u.close()}>{t.later}</Button>
      <Button icon="download" onclick={() => u.install()}>{t.install}</Button>
    {:else if working}
      <Button variant="ghost" disabled onclick={() => {}}>{t.later}</Button>
    {/if}
  {/snippet}
</Dialog>

<style>
  .sub,
  .muted {
    color: var(--muted);
  }

  .sub {
    margin-top: -8px;
    font-size: var(--fs-cap);
  }

  .notes {
    display: grid;
    gap: 6px;
    max-height: 200px;
    padding: 10px 12px;
    overflow-y: auto;
    border: 1px solid var(--line);
    border-radius: var(--r-ctl);
    background: var(--bg);
    user-select: text;
  }

  .notes .item {
    position: relative;
    padding-left: 14px;
  }

  .notes .item::before {
    content: '';
    position: absolute;
    top: 0.6em;
    left: 2px;
    width: 4px;
    height: 4px;
    border-radius: 50%;
    background: var(--muted);
  }

  .warn,
  .error {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 8px;
    align-items: start;
  }

  .warn :global(svg) {
    margin-top: 2px;
    color: var(--warn);
  }

  .error {
    color: var(--bad);
  }

  .error :global(svg) {
    margin-top: 2px;
  }

  .progress {
    display: grid;
    gap: 8px;
  }

  .line {
    display: flex;
    justify-content: space-between;
    gap: var(--s3);
  }

  .num {
    color: var(--muted);
    font-variant-numeric: tabular-nums;
  }

  .bar {
    height: 6px;
    overflow: hidden;
    border-radius: 3px;
    background: var(--surface-2);
  }

  .bar span {
    display: block;
    height: 100%;
    border-radius: 3px;
    background: var(--text);
    transition: width var(--fast) linear;
  }

  /* Without a size from the server the bar cannot fill; it runs instead. */
  .bar.loose span {
    animation: run 1.2s ease-in-out infinite;
  }

  @keyframes run {
    from {
      transform: translateX(-100%);
    }
    to {
      transform: translateX(340%);
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .bar span {
      transition: none;
    }

    .bar.loose span {
      animation: none;
    }
  }
</style>
