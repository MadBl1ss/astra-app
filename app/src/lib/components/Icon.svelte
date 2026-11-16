<script lang="ts" module>
  // Stroke icons on a 20 px grid, drawn for this app; the first five are the
  // ones of the first mockup of the window.
  const paths = {
    home: 'M3 10h5c3 0 3-5 6-5h3M8 10c3 0 3 5 6 5h3',
    servers:
      'M4.500 4h11A1.500 1.500 0 0117 5.500v2A1.500 1.500 0 0115.500 9h-11A1.500 1.500 0 013 7.500v-2A1.500 1.500 0 014.500 4zM4.500 11h11a1.500 1.500 0 011.500 1.500v2a1.500 1.500 0 01-1.500 1.500h-11A1.500 1.500 0 013 14.500v-2A1.500 1.500 0 014.500 11zM6 6.500h.01M6 13.500h.01',
    rules: 'M4 5h12M4 10h8M4 15h10',
    journal: 'M10 3a7 7 0 100 14 7 7 0 000-14zM10 6v4l2.500 2',
    settings:
      'M10 7.500a2.500 2.500 0 100 5 2.500 2.500 0 000-5zM10 2.500v2M10 15.500v2M2.500 10h2M15.500 10h2M4.700 4.700l1.400 1.400M13.900 13.900l1.400 1.400M4.700 15.300l1.400-1.400M13.900 6.100l1.400-1.400',
    shield: 'M10 2l7 3.500v5c0 3.500-2.800 6.200-7 7.500-4.200-1.300-7-4-7-7.500v-5zM6.500 11.500l3.500-3.500 3.500 3.500',
    lock: 'M5.500 9h9A1.500 1.500 0 0116 10.500v5a1.500 1.500 0 01-1.500 1.500h-9A1.500 1.500 0 014 15.500v-5A1.500 1.500 0 015.500 9zM7 9V6.500a3 3 0 016 0V9',
    unlock: 'M5.500 9h9A1.500 1.500 0 0116 10.500v5a1.500 1.500 0 01-1.500 1.500h-9A1.500 1.500 0 014 15.500v-5A1.500 1.500 0 015.500 9zM7 9V6.500a3 3 0 015.800-1',
    alert: 'M10 3.500l7.500 13h-15zM10 8.500v3.500M10 14.500h.01',
    stop: 'M10 3a7 7 0 100 14 7 7 0 000-14zM5.200 5.200l9.600 9.600',
    info: 'M10 3a7 7 0 100 14 7 7 0 000-14zM10 9.500V14M10 6.500h.01',
    chevron: 'M6 8l4 4 4-4',
    check: 'M4.500 10.500l3.500 3.500 7.500-8',
    minimize: 'M5 10h10',
    close: 'M5.500 5.500l9 9M14.500 5.500l-9 9',
    plug: 'M7 3v4M13 3v4M5 7h10v2a5 5 0 01-10 0zM10 14v3',
  } as const;

  export type IconName = keyof typeof paths;
</script>

<script lang="ts">
  let { name, size = 18 }: { name: IconName; size?: number } = $props();
</script>

<svg width={size} height={size} viewBox="0 0 20 20" aria-hidden="true">
  <path d={paths[name]} />
</svg>

<style>
  svg {
    flex: none;
    fill: none;
    stroke: currentColor;
    stroke-width: 1.6;
    stroke-linecap: round;
    stroke-linejoin: round;
  }
</style>
