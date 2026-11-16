<script lang="ts">
  let {
    checked,
    label,
    size = 'lg',
    disabled = false,
    onchange,
  }: {
    checked: boolean;
    /** Read by screen readers; the visible caption is next to the switch. */
    label: string;
    size?: 'lg' | 'sm';
    disabled?: boolean;
    onchange: (checked: boolean) => void;
  } = $props();
</script>

<button
  type="button"
  role="switch"
  class="switch {size}"
  aria-checked={checked}
  aria-label={label}
  {disabled}
  onclick={() => onchange(!checked)}
></button>

<style>
  .switch {
    --w: 52px;
    --h: 30px;
    --knob: 22px;
    position: relative;
    flex: none;
    width: var(--w);
    height: var(--h);
    padding: 0;
    border: 1px solid var(--line);
    border-radius: calc(var(--h) / 2);
    background: var(--surface-2);
    transition: border-color var(--slow);
  }

  .sm {
    --w: 36px;
    --h: 22px;
    --knob: 14px;
  }

  .switch::after {
    content: '';
    position: absolute;
    top: 3px;
    left: 3px;
    width: var(--knob);
    height: var(--knob);
    border-radius: 50%;
    background: var(--muted);
    transition:
      transform var(--slow) ease-out,
      background var(--slow);
  }

  /* Neutral on purpose: green means "traffic goes through the VPN and all
     is well", and a switch that is on promises no such thing. */
  .switch[aria-checked='true'] {
    border-color: var(--muted);
  }

  .switch[aria-checked='true']::after {
    transform: translateX(calc(var(--w) - var(--knob) - 8px));
    background: var(--text);
  }

  .switch:disabled {
    opacity: 0.5;
  }
</style>
