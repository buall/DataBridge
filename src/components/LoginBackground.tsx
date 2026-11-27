import { useEffect, useRef } from "react";
import { useTheme } from "./UI";

interface Ripple {
  x: number;
  y: number;
  radius: number;
  maxRadius: number;
  alpha: number;
}

interface FlowPulse {
  x: number;
  y: number;
  angle: number;
  speed: number;
  turnSeed: number;
  turnFreq: number;
  length: number;
  hue: "indigo" | "cyan" | "violet" | "blue";
  trail: Array<{ x: number; y: number }>;
  life: number;
  maxLife: number;
}

interface Spark {
  x: number;
  y: number;
  vx: number;
  vy: number;
  life: number;
  maxLife: number;
  hue: "indigo" | "cyan" | "violet";
}

export function LoginBackground() {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const { theme } = useTheme();
  const themeRef = useRef(theme);
  themeRef.current = theme;

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    let width = 0;
    let height = 0;
    let dpr = 1;
    let rafId = 0;
    const mouse = { x: -9999, y: -9999, tx: -9999, ty: -9999, active: false };
    const ripples: Ripple[] = [];
    const pulses: FlowPulse[] = [];
    const sparks: Spark[] = [];
    const gap = 36;
    const radius = 270;

    function getOffsets() {
      return {
        startX: (width % gap) * 0.5,
        startY: (height % gap) * 0.5,
      };
    }

    function spawnPulse() {
      const hues: Array<"indigo" | "cyan" | "violet" | "blue"> = ["indigo", "cyan", "violet", "blue"];
      const edge = Math.floor(Math.random() * 4);
      let x = Math.random() * width;
      let y = Math.random() * height;
      let angle = Math.random() * Math.PI * 2;

      if (Math.random() < 0.7) {
        if (edge === 0) {
          x = -20;
          angle = (Math.random() - 0.5) * 1.1;
        } else if (edge === 1) {
          x = width + 20;
          angle = Math.PI + (Math.random() - 0.5) * 1.1;
        } else if (edge === 2) {
          y = -20;
          angle = Math.PI * 0.5 + (Math.random() - 0.5) * 1.1;
        } else {
          y = height + 20;
          angle = -Math.PI * 0.5 + (Math.random() - 0.5) * 1.1;
        }
      }

      pulses.push({
        x,
        y,
        angle,
        speed: 2.2 + Math.random() * 2.4,
        turnSeed: Math.random() * Math.PI * 2,
        turnFreq: 0.018 + Math.random() * 0.025,
        length: 20 + Math.floor(Math.random() * 16),
        hue: hues[Math.floor(Math.random() * hues.length)],
        trail: [],
        life: 0,
        maxLife: 240 + Math.floor(Math.random() * 220),
      });
    }

    function resize() {
      if (!canvas) return;
      dpr = Math.min(window.devicePixelRatio || 1, 2);
      width = window.innerWidth;
      height = window.innerHeight;
      canvas.width = Math.floor(width * dpr);
      canvas.height = Math.floor(height * dpr);
      canvas.style.width = `${width}px`;
      canvas.style.height = `${height}px`;
      ctx?.setTransform(dpr, 0, 0, dpr, 0, 0);
      pulses.length = 0;
      if (!reducedMotion) {
        for (let i = 0; i < 14; i++) spawnPulse();
      }
    }

    function onMouseMove(e: MouseEvent) {
      mouse.tx = e.clientX;
      mouse.ty = e.clientY;
      if (!mouse.active) {
        mouse.x = e.clientX;
        mouse.y = e.clientY;
      }
      mouse.active = true;
    }

    function onMouseLeave() {
      mouse.active = false;
    }

    function onClick(e: MouseEvent) {
      if (reducedMotion) return;
      ripples.push({
        x: e.clientX,
        y: e.clientY,
        radius: 0,
        maxRadius: 460,
        alpha: 1,
      });
      if (ripples.length > 6) ripples.shift();

      const hues: Array<"indigo" | "cyan" | "violet"> = ["indigo", "cyan", "violet"];
      for (let i = 0; i < 16; i++) {
        const angle = (Math.PI * 2 * i) / 16 + (Math.random() - 0.5) * 0.25;
        const spd = 2.4 + Math.random() * 3.8;
        sparks.push({
          x: e.clientX,
          y: e.clientY,
          vx: Math.cos(angle) * spd,
          vy: Math.sin(angle) * spd,
          life: 0,
          maxLife: 32 + Math.random() * 20,
          hue: hues[i % hues.length],
        });
      }
    }

    resize();
    window.addEventListener("resize", resize);
    window.addEventListener("mousemove", onMouseMove);
    window.addEventListener("mouseleave", onMouseLeave);
    window.addEventListener("click", onClick);

    let tick = 0;

    function draw() {
      if (!ctx) return;
      tick += 0.022;
      ctx.clearRect(0, 0, width, height);

      if (mouse.active) {
        mouse.x += (mouse.tx - mouse.x) * 0.16;
        mouse.y += (mouse.ty - mouse.y) * 0.16;
      }

      const isDark = themeRef.current === "dark";
      const baseColor = isDark ? "148, 163, 184" : "100, 116, 139";
      const indigoColor = isDark ? "139, 130, 232" : "81, 70, 200";
      const cyanColor = isDark ? "56, 189, 248" : "14, 165, 233";
      const violetColor = isDark ? "192, 132, 252" : "124, 58, 237";
      const blueColor = isDark ? "96, 165, 250" : "37, 99, 235";

      const colorMap = {
        indigo: indigoColor,
        cyan: cyanColor,
        violet: violetColor,
        blue: blueColor,
      };

      // Rich drifting ambient aurora washes
      const orbs = [
        {
          x: width * (0.18 + Math.sin(tick * 0.42) * 0.1),
          y: height * (0.22 + Math.cos(tick * 0.35) * 0.1),
          r: Math.max(width, height) * 0.36,
          c: indigoColor,
          a: isDark ? 0.14 : 0.1,
        },
        {
          x: width * (0.82 + Math.cos(tick * 0.38) * 0.1),
          y: height * (0.76 + Math.sin(tick * 0.45) * 0.1),
          r: Math.max(width, height) * 0.34,
          c: cyanColor,
          a: isDark ? 0.12 : 0.09,
        },
        {
          x: width * (0.5 + Math.sin(tick * 0.3 + 2) * 0.18),
          y: height * (0.5 + Math.cos(tick * 0.32 + 1) * 0.14),
          r: Math.max(width, height) * 0.28,
          c: violetColor,
          a: isDark ? 0.09 : 0.065,
        },
      ];
      for (const orb of orbs) {
        const g = ctx.createRadialGradient(orb.x, orb.y, 0, orb.x, orb.y, orb.r);
        g.addColorStop(0, `rgba(${orb.c}, ${orb.a})`);
        g.addColorStop(1, `rgba(${orb.c}, 0)`);
        ctx.fillStyle = g;
        ctx.beginPath();
        ctx.arc(orb.x, orb.y, orb.r, 0, Math.PI * 2);
        ctx.fill();
      }

      const { startX, startY } = getOffsets();

      // Base subtle architectural grid
      ctx.strokeStyle = `rgba(${baseColor}, ${isDark ? 0.05 : 0.055})`;
      ctx.lineWidth = 1;
      ctx.beginPath();
      for (let x = startX; x < width; x += gap) {
        ctx.moveTo(x, 0);
        ctx.lineTo(x, height);
      }
      for (let y = startY; y < height; y += gap) {
        ctx.moveTo(0, y);
        ctx.lineTo(width, y);
      }
      ctx.stroke();

      // Spotlight illumination on grid lines
      if (mouse.active) {
        const glow = ctx.createRadialGradient(mouse.x, mouse.y, 0, mouse.x, mouse.y, radius);
        glow.addColorStop(0, `rgba(${cyanColor}, ${isDark ? 0.18 : 0.13})`);
        glow.addColorStop(0.45, `rgba(${indigoColor}, ${isDark ? 0.12 : 0.08})`);
        glow.addColorStop(1, `rgba(${indigoColor}, 0)`);
        ctx.fillStyle = glow;
        ctx.beginPath();
        ctx.arc(mouse.x, mouse.y, radius, 0, Math.PI * 2);
        ctx.fill();

        ctx.lineWidth = 1.2;
        for (let x = startX; x < width; x += gap) {
          const dx = Math.abs(x - mouse.x);
          if (dx < radius) {
            const lineStrength = (1 - dx / radius) * (isDark ? 0.34 : 0.24);
            const grad = ctx.createLinearGradient(x, mouse.y - radius, x, mouse.y + radius);
            grad.addColorStop(0, `rgba(${indigoColor}, 0)`);
            grad.addColorStop(0.5, `rgba(${indigoColor}, ${lineStrength.toFixed(3)})`);
            grad.addColorStop(1, `rgba(${indigoColor}, 0)`);
            ctx.strokeStyle = grad;
            ctx.beginPath();
            ctx.moveTo(x, Math.max(0, mouse.y - radius));
            ctx.lineTo(x, Math.min(height, mouse.y + radius));
            ctx.stroke();
          }
        }
        for (let y = startY; y < height; y += gap) {
          const dy = Math.abs(y - mouse.y);
          if (dy < radius) {
            const lineStrength = (1 - dy / radius) * (isDark ? 0.34 : 0.24);
            const grad = ctx.createLinearGradient(mouse.x - radius, y, mouse.x + radius, y);
            grad.addColorStop(0, `rgba(${cyanColor}, 0)`);
            grad.addColorStop(0.5, `rgba(${cyanColor}, ${lineStrength.toFixed(3)})`);
            grad.addColorStop(1, `rgba(${cyanColor}, 0)`);
            ctx.strokeStyle = grad;
            ctx.beginPath();
            ctx.moveTo(Math.max(0, mouse.x - radius), y);
            ctx.lineTo(Math.min(width, mouse.x + radius), y);
            ctx.stroke();
          }
        }
      }

      // Update & draw organic curved flow pulses
      if (!reducedMotion) {
        if (pulses.length < 16 && Math.random() < 0.08) spawnPulse();
        for (let i = pulses.length - 1; i >= 0; i--) {
          const p = pulses[i];
          p.trail.push({ x: p.x, y: p.y });
          if (p.trail.length > p.length) p.trail.shift();

          p.life++;
          p.angle += Math.sin(p.life * p.turnFreq + p.turnSeed) * 0.035;

          // Subtle curved deflection around cursor
          if (mouse.active) {
            const dx = p.x - mouse.x;
            const dy = p.y - mouse.y;
            const dist = Math.hypot(dx, dy);
            if (dist < radius * 0.85 && dist > 1) {
              const targetAngle = Math.atan2(dy, dx);
              const diff = Math.atan2(Math.sin(targetAngle - p.angle), Math.cos(targetAngle - p.angle));
              p.angle += diff * 0.03 * (1 - dist / (radius * 0.85));
            }
          }

          p.x += Math.cos(p.angle) * p.speed;
          p.y += Math.sin(p.angle) * p.speed;

          const lifeFade =
            Math.min(1, p.life / 24) * Math.min(1, Math.max(0, (p.maxLife - p.life) / 36));

          if (
            p.life >= p.maxLife ||
            p.x < -140 ||
            p.x > width + 140 ||
            p.y < -140 ||
            p.y > height + 140
          ) {
            pulses.splice(i, 1);
            continue;
          }

          const c = colorMap[p.hue];
          if (p.trail.length > 1) {
            for (let t = 1; t < p.trail.length; t++) {
              const frac = t / p.trail.length;
              ctx.strokeStyle = `rgba(${c}, ${(frac * lifeFade * (isDark ? 0.72 : 0.55)).toFixed(3)})`;
              ctx.lineWidth = 1.1 + frac * 1.6;
              ctx.beginPath();
              ctx.moveTo(p.trail[t - 1].x, p.trail[t - 1].y);
              ctx.lineTo(p.trail[t].x, p.trail[t].y);
              ctx.stroke();
            }
          }

          const headGlow = ctx.createRadialGradient(p.x, p.y, 0, p.x, p.y, 10);
          headGlow.addColorStop(0, `rgba(${c}, ${(lifeFade * (isDark ? 0.95 : 0.85)).toFixed(3)})`);
          headGlow.addColorStop(1, `rgba(${c}, 0)`);
          ctx.fillStyle = headGlow;
          ctx.beginPath();
          ctx.arc(p.x, p.y, 10, 0, Math.PI * 2);
          ctx.fill();
        }
      }

      // Update click ripples & draw shockwave ring
      for (let i = ripples.length - 1; i >= 0; i--) {
        const r = ripples[i];
        r.radius += 7.5;
        r.alpha = Math.max(0, 1 - r.radius / r.maxRadius);

        ctx.strokeStyle = `rgba(${cyanColor}, ${(r.alpha * (isDark ? 0.45 : 0.35)).toFixed(3)})`;
        ctx.lineWidth = 2;
        ctx.beginPath();
        ctx.arc(r.x, r.y, r.radius, 0, Math.PI * 2);
        ctx.stroke();

        if (r.radius >= r.maxRadius) ripples.splice(i, 1);
      }

      // Update & draw click burst sparks
      for (let i = sparks.length - 1; i >= 0; i--) {
        const s = sparks[i];
        s.x += s.vx;
        s.y += s.vy;
        s.vx *= 0.95;
        s.vy *= 0.95;
        s.life++;
        const a = Math.max(0, 1 - s.life / s.maxLife);
        const c = colorMap[s.hue];
        ctx.fillStyle = `rgba(${c}, ${(a * 0.9).toFixed(3)})`;
        ctx.beginPath();
        ctx.arc(s.x, s.y, 2.2 * a + 0.5, 0, Math.PI * 2);
        ctx.fill();
        if (s.life >= s.maxLife) sparks.splice(i, 1);
      }

      // Interactive geometric matrix (dots -> crosshairs -> glowing rotating diamonds)
      for (let x = startX; x < width; x += gap) {
        for (let y = startY; y < height; y += gap) {
          const wave = reducedMotion ? 0 : Math.sin(x * 0.011 + y * 0.011 - tick) * 0.5 + 0.5;
          let boost = 0;
          let pushX = 0;
          let pushY = 0;

          if (mouse.active) {
            const dx = x - mouse.x;
            const dy = y - mouse.y;
            const dist = Math.hypot(dx, dy);
            if (dist < radius && dist > 0.01) {
              const norm = 1 - dist / radius;
              boost = norm * norm;
              const push = Math.sin(norm * Math.PI) * 8;
              pushX += (dx / dist) * push;
              pushY += (dy / dist) * push;

              // Direct tether from cursor to very close nodes
              if (dist < 115) {
                const tetherA = (1 - dist / 115) * (isDark ? 0.35 : 0.24);
                ctx.strokeStyle = `rgba(${cyanColor}, ${tetherA.toFixed(3)})`;
                ctx.lineWidth = 1;
                ctx.beginPath();
                ctx.moveTo(mouse.x, mouse.y);
                ctx.lineTo(x + pushX, y + pushY);
                ctx.stroke();
              }
            }
          }

          let rippleBoost = 0;
          for (const r of ripples) {
            const dx = x - r.x;
            const dy = y - r.y;
            const dist = Math.hypot(dx, dy);
            const band = Math.abs(dist - r.radius);
            if (band < 52 && dist > 0.01) {
              const strength = (1 - band / 52) * r.alpha;
              rippleBoost = Math.max(rippleBoost, strength);
              pushX += (dx / dist) * strength * 9;
              pushY += (dy / dist) * strength * 9;
            }
          }

          const totalBoost = Math.min(1, boost + rippleBoost);
          const baseAlpha = (isDark ? 0.18 : 0.2) + wave * 0.08;
          const alpha = Math.min(0.98, baseAlpha + totalBoost * (isDark ? 0.75 : 0.65));
          const px = x + pushX;
          const py = y + pushY;

          if (totalBoost > 0.58) {
            // Rotating glowing diamond
            const c = rippleBoost > boost * 0.8 ? cyanColor : violetColor;
            const size = 4 + totalBoost * 3.5;
            ctx.save();
            ctx.translate(px, py);
            ctx.rotate(tick * 1.4 + totalBoost * Math.PI * 0.5);
            ctx.strokeStyle = `rgba(${c}, ${alpha.toFixed(3)})`;
            ctx.lineWidth = 1.4;
            ctx.strokeRect(-size * 0.5, -size * 0.5, size, size);
            ctx.restore();
          } else if (totalBoost > 0.24) {
            // Precision crosshair
            const c = totalBoost > 0.42 ? cyanColor : indigoColor;
            const arm = 2.6 + totalBoost * 2.8;
            ctx.strokeStyle = `rgba(${c}, ${alpha.toFixed(3)})`;
            ctx.lineWidth = 1.3;
            ctx.beginPath();
            ctx.moveTo(px - arm, py);
            ctx.lineTo(px + arm, py);
            ctx.moveTo(px, py - arm);
            ctx.lineTo(px, py + arm);
            ctx.stroke();
          } else {
            const dotRadius = 1.3 + wave * 0.45 + totalBoost * 1.6;
            ctx.fillStyle =
              totalBoost > 0.04
                ? `rgba(${indigoColor}, ${alpha.toFixed(3)})`
                : `rgba(${baseColor}, ${alpha.toFixed(3)})`;
            ctx.beginPath();
            ctx.arc(px, py, dotRadius, 0, Math.PI * 2);
            ctx.fill();
          }
        }
      }

      // Rotating holographic reticle around cursor
      if (mouse.active && !reducedMotion) {
        ctx.save();
        ctx.translate(mouse.x, mouse.y);
        ctx.rotate(tick * 0.8);
        ctx.strokeStyle = `rgba(${cyanColor}, ${isDark ? 0.35 : 0.25})`;
        ctx.lineWidth = 1.2;
        ctx.setLineDash([8, 10]);
        ctx.beginPath();
        ctx.arc(0, 0, 26, 0, Math.PI * 2);
        ctx.stroke();
        ctx.restore();
      }

      if (!reducedMotion) {
        rafId = window.requestAnimationFrame(draw);
      }
    }

    draw();

    return () => {
      window.cancelAnimationFrame(rafId);
      window.removeEventListener("resize", resize);
      window.removeEventListener("mousemove", onMouseMove);
      window.removeEventListener("mouseleave", onMouseLeave);
      window.removeEventListener("click", onClick);
    };
  }, []);

  return <canvas ref={canvasRef} className="login-bg" aria-hidden="true" />;
}
