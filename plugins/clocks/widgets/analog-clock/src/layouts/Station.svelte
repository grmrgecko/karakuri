<script lang="ts">
  let { hours = 0, minutes = 0, seconds = 0, faceStyle = "dark", accentColor = "", sweep = false }: { hours: number; minutes: number; seconds: number; faceStyle: string; accentColor: string; sweep: boolean; } = $props();

  let colors = $derived((() => {
    if (faceStyle === "light") {
      return { face: "#eff1f5", marks: "#4c4f69", hands: "#4c4f69", border: "#4c4f69" };
    }
    if (faceStyle === "transparent") {
      return { face: "none", marks: "currentColor", hands: "currentColor", border: "currentColor" };
    }
    return { face: "#1e1e2e", marks: "#cdd6f4", hands: "#cdd6f4", border: "#cdd6f4" };
  })());

  // The Swiss railway second hand is always red, ignoring accent color.
  const STATION_RED = "#e64553";

  let hourAngle = $derived((hours / 12) * 360);
  let minuteAngle = $derived((minutes / 60) * 360);
  let secondAngle = $derived((seconds / 60) * 360);

  // Twelve heavy rectangular hour markers placed around the dial.
  let hourMarkers = $derived(
    Array.from({ length: 12 }, (_, i) => {
      const angle = (i / 12) * 360;
      const rad = ((angle - 90) * Math.PI) / 180;
      const cx = 100 + 82 * Math.cos(rad);
      const cy = 100 + 82 * Math.sin(rad);
      return { cx, cy, angle };
    })
  );

  // Small minute dots between the hour markers for visual rhythm.
  let minuteDots = $derived(
    Array.from({ length: 60 }, (_, i) => {
      if (i % 5 === 0) return null;
      const angle = (i / 60) * 360;
      const rad = ((angle - 90) * Math.PI) / 180;
      const r = 88;
      return { cx: 100 + r * Math.cos(rad), cy: 100 + r * Math.sin(rad) };
    }).filter(Boolean)
  );
</script>

<svg viewBox="0 0 200 200" xmlns="http://www.w3.org/2000/svg" class="clock-svg">
  <!-- Face -->
  <circle cx="100" cy="100" r="95" fill={colors.face} stroke={colors.border} stroke-width="2.5" />

  <!-- Minute dots -->
  {#each minuteDots as dot}
    <circle cx={dot.cx} cy={dot.cy} r="1" fill={colors.marks} opacity="0.5" />
  {/each}

  <!-- Heavy rectangular hour markers -->
  {#each hourMarkers as marker}
    <rect
      x="-3.5" y="-8" width="7" height="16" rx="1"
      fill={colors.marks}
      transform="translate({marker.cx}, {marker.cy}) rotate({marker.angle})"
    />
  {/each}

  <!-- Hour hand — bold rectangular -->
  <rect
    x="-4" y="-50" width="8" height="54" rx="2"
    fill={colors.hands}
    transform="translate(100, 100) rotate({hourAngle})"
  />

  <!-- Minute hand — thinner rectangular, longer -->
  <rect
    x="-3" y="-70" width="6" height="74" rx="1.5"
    fill={colors.hands}
    transform="translate(100, 100) rotate({minuteAngle})"
  />

  <!-- Second hand — the distinctive lollipop design -->
  <g transform="translate(100, 100) rotate({secondAngle})">
    <!-- Tail -->
    <line x1="0" y1="18" x2="0" y2="-58" stroke={STATION_RED} stroke-width="1.5" stroke-linecap="round" />
    <!-- Lollipop circle near the tip -->
    <circle cx="0" cy="-62" r="5.5" fill={STATION_RED} />
  </g>

  <!-- Center cap -->
  <circle cx="100" cy="100" r="4" fill={colors.hands} />
</svg>

<style>
  .clock-svg {
    width: 100%;
    height: 100%;
    max-width: 100%;
    max-height: 100%;
    display: block;
  }
</style>
