<script lang="ts">
  import Icon from '../../components/Icon.svelte';
  import IconButton from '../../components/IconButton.svelte';
  import Stability from '../../components/Stability.svelte';
  import { ru } from '../../i18n/ru';
  import type { Server } from '../../proto';
  import { protocolLabel, type PingView } from '../../servers';
  import { displayName, flagOf } from '../../view';

  // One server: the whole row picks it, the star and (for a server added by
  // link) the bin are buttons of their own. In the favourites a grip on the
  // left drags the row; Alt+arrows on the row do the same from the keyboard.
  let {
    server,
    selected,
    ping,
    testing = false,
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
    /** A test of all servers is running: the numbers are about to change. */
    testing?: boolean;
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

<li class="row" class:selected class:dragging class:shifted={shift !== 0 && !dragging} style:transform={shift ? `translateY(${shift}px)` : undefined}>
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
    <span class="ping {ping.tone}" class:testing title={ping.title}>{ping.text}</span>
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

  .ping.testing {
    animation: breathe 1.2s ease-in-out infinite;
  }

  @keyframes breathe {
    50% {
      opacity: 0.35;
    }
  }

  .gap {
    flex: none;
    width: 28px;
  }
</style>
