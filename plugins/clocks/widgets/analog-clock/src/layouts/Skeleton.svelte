<script lang="ts">
  let { hours = 0, minutes = 0, seconds = 0, faceStyle = "dark", accentColor = "", sweep = false }: { hours: number; minutes: number; seconds: number; faceStyle: string; accentColor: string; sweep: boolean; } = $props();

  let colors = $derived((() => {
    if (faceStyle === "light") {
      return { face: "none", marks: "#4c4f69", hands: "#4c4f69", border: "#4c4f69", gear: "#dce0e8" };
    }
    if (faceStyle === "transparent") {
      return { face: "none", marks: "currentColor", hands: "currentColor", border: "currentColor", gear: "currentColor" };
    }
    return { face: "none", marks: "#cdd6f4", hands: "#cdd6f4", border: "#cdd6f4", gear: "#313244" };
  })());

  let accent = $derived(accentColor || "#89b4fa");
  let hourAngle = $derived((hours / 12) * 360);
  let minuteAngle = $derived((minutes / 60) * 360);
  let secondAngle = $derived((seconds / 60) * 360);

  // The hour gear completes one revolution per 12 hours, so it uses the raw
  // hours value directly to produce a slow rotation.
  let hourGearAngle = $derived((hours / 12) * 360);

  // The minute gear rotates once per hour, driven by the minutes value.
  let minuteGearAngle = $derived((minutes / 60) * 360);

  // Build an SVG path for a gear shape with a given number of teeth.
  function gearPath(cx, cy, innerR, outerR, teeth) {
    const step = (2 * Math.PI) / teeth;
    const toothHalf = step * 0.25;
    let d = "";
    for (let i = 0; i < teeth; i++) {
      const baseAngle = i * step - Math.PI / 2;
      // Inner arc start.
      const ix1 = cx + innerR * Math.cos(baseAngle - toothHalf);
      const iy1 = cy + innerR * Math.sin(baseAngle - toothHalf);
      // Outer tooth start.
      const ox1 = cx + outerR * Math.cos(baseAngle - toothHalf * 0.6);
      const oy1 = cy + outerR * Math.sin(baseAngle - toothHalf * 0.6);
      // Outer tooth end.
      const ox2 = cx + outerR * Math.cos(baseAngle + toothHalf * 0.6);
      const oy2 = cy + outerR * Math.sin(baseAngle + toothHalf * 0.6);
      // Inner arc end.
      const ix2 = cx + innerR * Math.cos(baseAngle + toothHalf);
      const iy2 = cy + innerR * Math.sin(baseAngle + toothHalf);

      if (i === 0) {
        d += `M ${ix1} ${iy1}`;
      } else {
        d += ` L ${ix1} ${iy1}`;
      }
      d += ` L ${ox1} ${oy1} L ${ox2} ${oy2} L ${ix2} ${iy2}`;
    }
    d += " Z";
    return d;
  }

  // Two gear shapes at different sizes to create visual depth.
  let largeGear = $derived(gearPath(0, 0, 28, 36, 16));
  let smallGear = $derived(gearPath(0, 0, 16, 22, 10));

  // Hour tick marks on the face perimeter.
  let hourTicks = $derived(
    Array.from({ length: 12 }, (_, i) => {
      const angle = (i / 12) * 360;
      const rad = ((angle - 90) * Math.PI) / 180;
      const outerR = 90;
      const innerR = 82;
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
  <!-- Outer ring -->
  <circle cx="100" cy="100" r="95" fill={colors.face} stroke={colors.border} stroke-width="1.5" />

  <!-- Large gear behind the hands, rotating once per 12 hours -->
  <g transform="translate(100, 100) rotate({hourGearAngle})">
    <path d={largeGear} fill="none" stroke={colors.gear} stroke-width="1.5" opacity="0.4" />
    <circle cx="0" cy="0" r="10" fill="none" stroke={colors.gear} stroke-width="1" opacity="0.3" />
  </g>

  <!-- Small gear offset to the upper-right, rotating once per hour -->
  <g transform="translate(130, 72) rotate({minuteGearAngle})">
    <path d={smallGear} fill="none" stroke={colors.gear} stroke-width="1.2" opacity="0.35" />
    <circle cx="0" cy="0" r="6" fill="none" stroke={colors.gear} stroke-width="0.8" opacity="0.25" />
  </g>

  <!-- Hour tick marks -->
  {#each hourTicks as tick}
    <line
      x1={tick.x1} y1={tick.y1} x2={tick.x2} y2={tick.y2}
      stroke={colors.marks} stroke-width="2" stroke-linecap="round"
    />
  {/each}

  <!-- Hour hand -->
  <line
    x1="100" y1="100" x2="100" y2="46"
    stroke={colors.hands} stroke-width="3.5" stroke-linecap="round"
    transform="rotate({hourAngle}, 100, 100)"
  />

  <!-- Minute hand -->
  <line
    x1="100" y1="100" x2="100" y2="24"
    stroke={colors.hands} stroke-width="2" stroke-linecap="round"
    transform="rotate({minuteAngle}, 100, 100)"
  />

  <!-- Second hand -->
  <line
    x1="100" y1="116" x2="100" y2="20"
    stroke={accent} stroke-width="0.8" stroke-linecap="round"
    transform="rotate({secondAngle}, 100, 100)"
  />

  <!-- Center cap -->
  <circle cx="100" cy="100" r="3" fill={accent} />
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
