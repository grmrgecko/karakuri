<script lang="ts">
  let { hours = 0, minutes = 0, seconds = 0, faceStyle = "dark", accentColor = "", sweep = false }: { hours: number; minutes: number; seconds: number; faceStyle: string; accentColor: string; sweep: boolean; } = $props();

  // Map face style to color values for the SVG elements.
  let colors = $derived((() => {
    if (faceStyle === "light") {
      return { face: "#eff1f5", marks: "#4c4f69", hands: "#4c4f69", border: "#4c4f69" };
    }
    if (faceStyle === "transparent") {
      return { face: "none", marks: "currentColor", hands: "currentColor", border: "currentColor" };
    }
    return { face: "#1e1e2e", marks: "#cdd6f4", hands: "#cdd6f4", border: "#cdd6f4" };
  })());

  let accent = $derived(accentColor || "#89b4fa");
  let hourAngle = $derived((hours / 12) * 360);
  let minuteAngle = $derived((minutes / 60) * 360);
  let secondAngle = $derived((seconds / 60) * 360);

  // Pre-compute minute tick mark positions around the dial edge.
  let minuteTicks = $derived(
    Array.from({ length: 60 }, (_, i) => {
      const angle = (i / 60) * 360;
      const isHour = i % 5 === 0;
      const outerR = 90;
      const innerR = isHour ? 80 : 85;
      const rad = ((angle - 90) * Math.PI) / 180;
      return {
        x1: 100 + innerR * Math.cos(rad),
        y1: 100 + innerR * Math.sin(rad),
        x2: 100 + outerR * Math.cos(rad),
        y2: 100 + outerR * Math.sin(rad),
        isHour,
      };
    })
  );

  // Pre-compute number label positions placed inside the hour markers.
  let numberLabels = $derived(
    Array.from({ length: 12 }, (_, i) => {
      const num = i + 1;
      const angle = (num / 12) * 360;
      const rad = ((angle - 90) * Math.PI) / 180;
      const r = 72;
      return { num, x: 100 + r * Math.cos(rad), y: 100 + r * Math.sin(rad) };
    })
  );
</script>

<svg viewBox="0 0 200 200" xmlns="http://www.w3.org/2000/svg" class="clock-svg">
  <!-- Face -->
  <circle cx="100" cy="100" r="95" fill={colors.face} stroke={colors.border} stroke-width="2" />

  <!-- Minute and hour tick marks -->
  {#each minuteTicks as tick}
    <line
      x1={tick.x1} y1={tick.y1} x2={tick.x2} y2={tick.y2}
      stroke={colors.marks}
      stroke-width={tick.isHour ? 2.5 : 0.8}
      stroke-linecap="round"
    />
  {/each}

  <!-- Number labels -->
  {#each numberLabels as label}
    <text
      x={label.x} y={label.y}
      text-anchor="middle" dominant-baseline="central"
      fill={colors.marks} font-size="12" font-family="inherit" font-weight="500"
    >{label.num}</text>
  {/each}

  <!-- Hour hand -->
  <line
    x1="100" y1="100" x2="100" y2="45"
    stroke={colors.hands} stroke-width="4.5" stroke-linecap="round"
    transform="rotate({hourAngle}, 100, 100)"
  />

  <!-- Minute hand -->
  <line
    x1="100" y1="100" x2="100" y2="25"
    stroke={colors.hands} stroke-width="2.5" stroke-linecap="round"
    transform="rotate({minuteAngle}, 100, 100)"
  />

  <!-- Second hand with tail extension -->
  <line
    x1="100" y1="120" x2="100" y2="18"
    stroke={accent} stroke-width="1" stroke-linecap="round"
    transform="rotate({secondAngle}, 100, 100)"
  />

  <!-- Center cap -->
  <circle cx="100" cy="100" r="3.5" fill={accent} />
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
