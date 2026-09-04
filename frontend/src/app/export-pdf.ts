import type { Place, PlanResponse } from './api';

function wrapText(doc: any, text: string, x: number, y: number, maxWidth: number, lineHeight: number): number {
  const lines: string[] = doc.splitTextToSize(text, maxWidth);
  lines.forEach((line: string, i: number) => {
    doc.text(line, x, y + i * lineHeight);
  });
  return lines.length * lineHeight;
}

function drawCurveArrow(doc: any, x: number, y: number, width: number, height: number, flip: boolean): void {
  const midX = flip ? x + width * 0.72 : x + width * 0.28;
  const startX = x + width / 2;
  const endX = x + width / 2;
  const startY = y + 4;
  const endY = y + height - 4;

  doc.setDrawColor(26, 115, 232);
  doc.setLineWidth(1.2);
  // Approximate curve with several segments
  const steps = 18;
  let prevX = startX;
  let prevY = startY;
  for (let i = 1; i <= steps; i++) {
    const t = i / steps;
    const mt = 1 - t;
    const cx = mt * mt * startX + 2 * mt * t * midX + t * t * endX;
    const cy = mt * mt * startY + 2 * mt * t * (y + height / 2) + t * t * endY;
    doc.line(prevX, prevY, cx, cy);
    prevX = cx;
    prevY = cy;
  }

  // Arrow head
  const ah = 4;
  doc.setFillColor(26, 115, 232);
  if (flip) {
    doc.triangle(endX, endY, endX - ah, endY - ah, endX + ah * 0.3, endY - ah, 'F');
  } else {
    doc.triangle(endX, endY, endX - ah * 0.3, endY - ah, endX + ah, endY - ah, 'F');
  }
}

export async function exportRoutePdf(
  result: PlanResponse,
  labels: { duration: string; distance: string; role: (i: number, n: number) => string },
): Promise<void> {
  const { jsPDF } = await import('jspdf');
  const doc = new jsPDF({ unit: 'pt', format: 'a4' });
  const pageW = doc.internal.pageSize.getWidth();
  const pageH = doc.internal.pageSize.getHeight();
  const margin = 40;
  const contentW = pageW - margin * 2;
  let y = margin;

  const ensureSpace = (need: number) => {
    if (y + need > pageH - margin) {
      doc.addPage();
      y = margin;
    }
  };

  // Header
  doc.setFillColor(245, 248, 252);
  doc.rect(0, 0, pageW, 110, 'F');
  doc.setTextColor(26, 115, 232);
  doc.setFontSize(11);
  doc.setFont('helvetica', 'bold');
  doc.text('WORLD ROUTE', margin, 36);
  doc.setTextColor(15, 23, 42);
  doc.setFontSize(22);
  doc.text('Your optimized trip', margin, 62);
  doc.setFont('helvetica', 'normal');
  doc.setFontSize(12);
  doc.setTextColor(91, 101, 117);
  doc.text(`${labels.duration}  ·  ${labels.distance}`, margin, 84);
  y = 130;

  if (result.warning) {
    ensureSpace(40);
    doc.setFontSize(9);
    doc.setTextColor(100, 116, 139);
    y += wrapText(doc, result.warning, margin, y, contentW, 12) + 12;
  }

  const ordered = result.ordered;
  const legs = result.legs || [];

  ordered.forEach((place: Place, i: number) => {
    const isStart = i === 0;
    const isEnd = i === ordered.length - 1;
    const cardH = 58;
    ensureSpace(cardH + 70);

    // Place card
    doc.setDrawColor(226, 232, 240);
    doc.setFillColor(255, 255, 255);
    if (isStart) {
      doc.setDrawColor(183, 228, 199);
      doc.setFillColor(243, 255, 247);
    } else if (isEnd) {
      doc.setDrawColor(245, 194, 188);
      doc.setFillColor(255, 245, 244);
    }
    doc.roundedRect(margin, y, contentW, cardH, 10, 10, 'FD');

    // Accent bar
    if (isStart) doc.setFillColor(52, 168, 83);
    else if (isEnd) doc.setFillColor(234, 67, 53);
    else doc.setFillColor(251, 188, 4);
    doc.roundedRect(margin + 10, y + 14, 8, 30, 3, 3, 'F');

    doc.setTextColor(100, 116, 139);
    doc.setFontSize(8);
    doc.setFont('helvetica', 'bold');
    doc.text(labels.role(i, ordered.length).toUpperCase(), margin + 28, y + 22);

    doc.setTextColor(15, 23, 42);
    doc.setFontSize(13);
    doc.setFont('helvetica', 'bold');
    wrapText(doc, place.name || 'Stop', margin + 28, y + 40, contentW - 48, 14);

    y += cardH + 4;

    // Curved leg connector
    if (!isEnd && legs[i]) {
      const legH = 56;
      ensureSpace(legH);
      const flip = i % 2 === 1;
      drawCurveArrow(doc, margin, y, contentW, legH, flip);

      const bubbleW = 120;
      const bubbleX = flip ? margin + contentW * 0.18 : margin + contentW * 0.52;
      const bubbleY = y + legH / 2 - 16;
      doc.setFillColor(238, 244, 255);
      doc.setDrawColor(201, 218, 248);
      doc.roundedRect(bubbleX, bubbleY, bubbleW, 32, 10, 10, 'FD');
      doc.setFont('helvetica', 'bold');
      doc.setFontSize(10);
      doc.setTextColor(15, 23, 42);
      const dist = formatDistance(legs[i].distanceM);
      const dur = formatDuration(legs[i].durationS);
      doc.text(dist, bubbleX + 14, bubbleY + 13);
      doc.setFont('helvetica', 'normal');
      doc.setFontSize(8);
      doc.setTextColor(100, 116, 139);
      doc.text(dur, bubbleX + 14, bubbleY + 24);

      y += legH;
    }
  });

  y += 16;
  ensureSpace(36);
  doc.setFontSize(8);
  doc.setTextColor(148, 163, 184);
  doc.text(`Exported from World Route · ${new Date().toLocaleString()}`, margin, y);

  const stamp = new Date().toISOString().slice(0, 10);
  doc.save(`world-route-${stamp}.pdf`);
}

function formatDistance(meters: number): string {
  const km = meters / 1000;
  if (km >= 100) return `${Math.round(km)} km`;
  if (km >= 1) return `${km.toFixed(1)} km`;
  return `${Math.round(meters)} m`;
}

function formatDuration(seconds: number): string {
  const s = Math.round(seconds);
  const h = Math.floor(s / 3600);
  const m = Math.round((s % 3600) / 60);
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m} min`;
  return '<1 min';
}
