<script lang="ts">
  import Icon from '../../components/Icon.svelte';
  import IconButton from '../../components/IconButton.svelte';
  import Stability from '../../components/Stability.svelte';
  import { ru } from '../../i18n/ru';
  import type { Check, Server } from '../../proto';
  import { protocolLabel, type PingView } from '../../servers';
  import { displayName, flagOf } from '../../view';

  // One server: the whole row picks it, the star and (for a server added by
  // link) the bin are buttons of their own. In the favourites a grip on the
  // left drags the row; Alt+arrows on the row do the same from the keyboard.
  let {
    server,
    selected,
    ping,
    check = null,
    fresh = false,
    shift = 0,
    dragging = false,
    grip = false,
    onselect,
    onstar,
    onremove,
    ongrip,
    onkeydown,
  }: {
    server: Server;
    selected: boolean;
    ping: PingView;
    /** Where the server is in the test under way. */
    check?: Check | null;
    /** Its result came in this round: it lands with a flash. */
    fresh?: boolean;
    /** Pixels to slide by while another row is dragged past this one. */
    shift?: number;
    dragging?: boolean;
    grip?: boolean;
    onselect: () => void;
    onstar: () => void;
    onremove?: () => void;
    ongrip?: (event: PointerEvent) => void;
    onkeydown?: (event: KeyboardEvent) => void;
  } = $props();

  const t = ru.servers;
  const name = $derived(displayName(server));
  const flag = $derived(flagOf(server));
</script>

<li
  class="row"
  class:selected
  class:dragging
  class:shifted={shift !== 0 && !dragging}
  class:running={check === 'running'}
  class:landed={fresh && (check === null || check === 'done')}
  class:miss={fresh && !server.alive}
  style:transform={shift ? `translateY(${shift}px)` : undefined}
>
  {#if grip}
    <span class="grip" title={t.drag} aria-hidden="true" onpointerdown={ongrip}><Icon name="grip" size={14} /></span>
  {/if}
  <button type="button" class="pick" data-id={server.id} aria-current={selected ? 'true' : undefined} onclick={onselect} {onkeydown}>
    <span class="mark" aria-hidden="true">{#if selected}<Icon name="check" size={14} />{/if}</span>
    {#if selected}<span class="sr-only">{t.selected}:</span>{/if}
    <span class="flag">{flag ?? ''}</span>
    <span class="name" title={server.name !== name ? server.name : undefined}>{name}</span>
    <span class="proto" title={server.via === 'xray' ? `${protocolLabel(server)}, ${t.viaXray}` : undefined}>{protocolLabel(server)}</span>
    <Stability stats={server.stats} />
    {#if check === 'queued'}
      <span class="ping" title={t.checkQueued}><span class="slot" aria-hidden="true"></span><span class="sr-only">{t.checkQueued}</span></span>
    {:else if check === 'running'}
      <span class="ping" title={t.checkRunning}>
        <span class="dots" aria-hidden="true"><i></i><i></i><i></i></span><span class="sr-only">{t.checkRunning}</span>
      </span>
    {:else}
      <span class="ping {ping.tone}" class:fresh title={ping.title}>{ping.text}</span>
    {/if}
  </button>
  <IconButton
    icon="star"
    fill={server.favorite}
    pressed={server.favorite}
    label={server.favorite ? t.favoriteRemove(name) : t.favoriteAdd(name)}
    onclick={onstar}
  />
  {#if onremove}
    <IconButton icon="trash" tone="bad" label={t.removeServer(name)} onclick={onremove} />
  {:else}
    <span class="gap"></span>
  {/if}
</li>

<style>
  .row {
    position: relative;
    display: flex;
    align-items: center;
    gap: 2px;
    height: var(--row);
    padding-right: 4px;
    border-top: 1px solid var(--line);
    background: var(--surface);
  }

  .row:first-child {
    border-top-color: transparent;
  }

  .shifted {
    transition: transform var(--fast) ease-out;
  }

  .dragging {
    z-index: 2;
    border-radius: var(--r-ctl);
    box-shadow: var(--shadow-pop);
  }

  .selected {
    background: var(--surface-2);
  }

  /* Picked is said by a mark, a word for readers and a bar, not by colour. */
  .selected::before {
    content: '';
    position: absolute;
    top: 6px;
    bottom: 6px;
    left: 0;
    width: 2px;
    border-radius: 1px;
    background: var(--text);
  }

  .grip {
    display: grid;
    flex: none;
    place-items: center;
    width: 20px;
    height: 100%;
    color: var(--muted);
    cursor: grab;
    touch-action: none;
  }

  .dragging .grip {
    cursor: grabbing;
  }

  .pick {
    display: flex;
    flex: 1;
    align-items: center;
    gap: var(--s2);
    min-width: 0;
    height: 100%;
    padding: 0 8px 0 6px;
    border: 0;
    border-radius: var(--r-ctl);
    background: transparent;
    text-align: left;
  }

  .row:not(.selected) .pick:hover {
    background: color-mix(in srgb, var(--surface-2) 60%, transparent);
  }

  .pick:focus-visible {
    outline-offset: -2px;
  }

  .mark {
    display: flex;
    flex: none;
    width: 14px;
    color: var(--text);
  }

  .flag {
    flex: none;
    width: 20px;
    font-size: 15px;
    line-height: 1;
    text-align: center;
  }

  .name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .proto {
    flex: none;
    width: 52px;
    color: var(--muted);
    font-size: var(--fs-cap);
  }

  .ping {
    flex: none;
    width: 62px;
    color: var(--muted);
    text-align: right;
    white-space: nowrap;
  }

  .ping.ok {
    color: var(--vpn);
  }

  .ping.warn {
    color: var(--warn);
  }

  .ping.bad {
    color: var(--bad);
  }

  /* The test, row by row. A queued server holds an empty slot where its
     number will be; the one being tested has dots that hop and a light
     that runs along the row; a result lands into the slot and the row
     flashes once, red-tinted when the server did not answer. */
  .slot {
    display: inline-block;
    width: 22px;
    height: 4px;
    border-radius: 2px;
    background: var(--line);
    vertical-align: middle;
  }

  .dots {
    display: inline-flex;
    gap: 3px;
    vertical-align: middle;
  }

  .dots i {
    width: 4px;
    height: 4px;
    border-radius: 50%;
    background: var(--muted);
    animation: hop 0.9s ease-in-out infinite;
  }

  .dots i:nth-child(2) {
    animation-delay: 0.15s;
  }

  .dots i:nth-child(3) {
    animation-delay: 0.3s;
  }

  @keyframes hop {
    0%,
    70%,
    100% {
      opacity: 0.3;
      transform: translateY(0);
    }
    35% {
      opacity: 1;
      transform: translateY(-3px);
    }
  }

  .running::after,
  .landed::after {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    pointer-events: none;
  }

  .running::after {
    background: linear-gradient(90deg, transparent, color-mix(in srgb, var(--text) 7%, transparent), transparent) no-repeat;
    background-size: 35% 100%;
    animation: sweep 1.3s ease-in-out infinite;
  }

  @keyframes sweep {
    from {
      background-position: -60% 0;
    }
    to {
      background-position: 160% 0;
    }
  }

  .landed::after {
    background: color-mix(in srgb, var(--text) 9%, transparent);
    animation: flash 0.9s ease-out forwards;
  }

  .landed.miss::after {
    background: color-mix(in srgb, var(--bad) 14%, transparent);
  }

  @keyframes flash {
    to {
      opacity: 0;
    }
  }

  .ping.fresh {
    animation: land 0.32s cubic-bezier(0.2, 0.8, 0.2, 1) both;
  }

  @keyframes land {
    from {
      opacity: 0;
      transform: translateY(5px) scale(0.9);
    }
  }

  .gap {
    flex: none;
    width: 28px;
  }
</style>
