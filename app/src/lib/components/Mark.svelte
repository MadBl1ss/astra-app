<script lang="ts">
  import { MARK_CUTS, MARK_H, MARK_OUTLINE, MARK_W } from '../mark';

  /** `size` is the side of a square, like Icon's: the mark is centred in it. */
  let { size = 16 }: { size?: number } = $props();

  // Two marks on one page (the title bar and the first screen) must not
  // share a mask.
  const id = $props.id();
  const pad = (MARK_W - MARK_H) / 2;
</script>

<svg width={size} height={size} viewBox="0 {-pad} {MARK_W} {MARK_W}" aria-hidden="true" focusable="false">
  <defs>
    <mask id="mark-{id}" maskUnits="userSpaceOnUse" x="-20" y="-20" width="660" height="460">
      <path d={MARK_OUTLINE} fill="#fff" />
      {#each MARK_CUTS as d (d)}
        <path {d} fill="#000" />
      {/each}
    </mask>
  </defs>
  <rect x="-20" y="-20" width="660" height="460" fill="currentColor" mask="url(#mark-{id})" />
</svg>

<style>
  svg {
    display: block;
    flex: none;
  }
</style>
