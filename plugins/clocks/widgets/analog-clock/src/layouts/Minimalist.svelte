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

  let accent = $derived(accentColor || "#89b4fa");
  let hourAngle = $derived((hours / 12) * 360);
  let minuteAngle = $derived((minutes / 60) * 360);
  let secondAngle = $derived((seconds / 60) * 360);

  // Twelve evenly spaced thin tick marks for hours only.
  let hourTicks = $derived(
    Array.from({ length: 12 }, (_, i) => {
      const angle = (i / 12) * 360;
      const rad = ((angle - 90) * Math.PI) / 180;
      const outerR = 90;
      const innerR = 84;
      return {
        x1: 100 + innerR * Math.cos(rad),
        y1: 100 + innerR * Math.sin(rad),
        x2: 100 + outerR * Math.cos(rad),
        y2: 100 + outerR * Math.sin(rad),
      };
    })
  );
</script>

<svg viewBox="0 0 200 200" xmlns="http://www.w3.org/2000/svg" class="clock-svg">
  <!-- Face -->
  <circle cx="100" cy="100" r="95" fill={colors.face} stroke={colors.border} stroke-width="1.5" />

  <!-- Hour tick marks -->
  {#each hourTicks as tick}
    <line
      x1={tick.x1} y1={tick.y1} x2={tick.x2} y2={tick.y2}
      stroke={colors.marks} stroke-width="1.2" stroke-linecap="round"
    />
  {/each}

  <!-- Hour hand — slim and elegant -->
  <line
    x1="100" y1="100" x2="100" y2="48"
    stroke={colors.hands} stroke-width="2.5" stroke-linecap="round"
    transform="rotate({hourAngle}, 100, 100)"
  />

  <!-- Minute hand — thinner and longer -->
  <line
    x1="100" y1="100" x2="100" y2="24"
    stroke={colors.hands} stroke-width="1.5" stroke-linecap="round"
    transform="rotate({minuteAngle}, 100, 100)"
  />

  <!-- Thin second hand -->
  <line
    x1="100" y1="115" x2="100" y2="22"
    stroke={accent} stroke-width="0.7" stroke-linecap="round"
    transform="rotate({secondAngle}, 100, 100)"
  />

  <!-- Center dot -->
  <circle cx="100" cy="100" r="2.5" fill={accent} />
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
