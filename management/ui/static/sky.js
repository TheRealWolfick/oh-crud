// The drifting constellation behind the sidebar and the page, from the B3 design.
// Decoration only: it stays still for people who prefer reduced motion.
(function () {
  const looks = {
    side: { dot: '255,214,180', line: '245,158,91', glow: '245,158,91', dotAlpha: 0.85, lineAlpha: 0.3, density: 4200, max: 60, reach: 110 },
    main: { dot: '178,60,10', line: '194,65,12', glow: '245,158,91', dotAlpha: 0.5, lineAlpha: 0.2, density: 7000, max: 110, reach: 130 },
    // The sign-in page fills the whole screen, so it needs far more stars for them to
    // come within reach of each other and draw lines often
    login: { dot: '255,214,180', line: '245,158,91', glow: '245,158,91', dotAlpha: 0.85, lineAlpha: 0.38, density: 4500, max: 260, reach: 150 },
  };
  const still = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  function run(el, o) {
    const ctx = el.getContext('2d');
    if (!ctx) return;
    let w = 0, h = 0, stars = [];
    const seed = () => {
      const n = Math.round(Math.min(o.max, (w * h) / o.density));
      stars = [];
      for (let i = 0; i < n; i++) {
        stars.push({ x: Math.random() * w, y: Math.random() * h, vx: (Math.random() - 0.5) * 0.14, vy: (Math.random() - 0.5) * 0.08,
          r: 0.7 + Math.random() * 1.5, p: Math.random() * Math.PI * 2, s: 0.004 + Math.random() * 0.012, a: 0 });
      }
    };
    const fit = () => {
      const dpr = window.devicePixelRatio || 1;
      const b = el.getBoundingClientRect();
      w = b.width; h = b.height;
      el.width = Math.round(w * dpr); el.height = Math.round(h * dpr);
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      seed();
    };
    const draw = () => {
      ctx.clearRect(0, 0, w, h);
      for (const s of stars) {
        if (!still) {
          s.x += s.vx; s.y += s.vy; s.p += s.s;
          if (s.x < -20) s.x = w + 20; else if (s.x > w + 20) s.x = -20;
          if (s.y < -20) s.y = h + 20; else if (s.y > h + 20) s.y = -20;
        }
        s.a = 0.1 + 0.9 * (0.5 + 0.5 * Math.sin(s.p));
      }
      ctx.lineWidth = 1;
      for (let i = 0; i < stars.length; i++) {
        const a = stars[i];
        for (let j = i + 1; j < stars.length; j++) {
          const b = stars[j];
          const dx = a.x - b.x, dy = a.y - b.y, d2 = dx * dx + dy * dy;
          if (d2 > o.reach * o.reach) continue;
          const op = (1 - Math.sqrt(d2) / o.reach) * Math.min(a.a, b.a) * o.lineAlpha;
          ctx.strokeStyle = 'rgba(' + o.line + ',' + op.toFixed(3) + ')';
          ctx.beginPath(); ctx.moveTo(a.x, a.y); ctx.lineTo(b.x, b.y); ctx.stroke();
        }
      }
      for (const s of stars) {
        if (s.r > 1.7) {
          ctx.fillStyle = 'rgba(' + o.glow + ',' + (s.a * 0.2).toFixed(3) + ')';
          ctx.beginPath(); ctx.arc(s.x, s.y, s.r * 3.4, 0, Math.PI * 2); ctx.fill();
        }
        ctx.fillStyle = 'rgba(' + o.dot + ',' + (s.a * o.dotAlpha).toFixed(3) + ')';
        ctx.beginPath(); ctx.arc(s.x, s.y, s.r, 0, Math.PI * 2); ctx.fill();
      }
      if (!still) requestAnimationFrame(draw);
    };
    fit();
    if (window.ResizeObserver) new ResizeObserver(() => { fit(); if (still) draw(); }).observe(el);
    draw();
  }

  document.querySelectorAll('canvas[data-sky]').forEach((el) => run(el, looks[el.dataset.sky] || looks.main));
})();
