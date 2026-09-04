import { CommonModule } from '@angular/common';
import { HttpErrorResponse } from '@angular/common/http';
import { ChangeDetectorRef, Component, DestroyRef, NgZone, OnDestroy, OnInit, inject } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { Subject, debounceTime, distinctUntilChanged, filter, switchMap } from 'rxjs';
import mapboxgl from './mapbox';
import { ApiService, Place, PlanResponse, SearchHit } from './api';
import { exportRoutePdf } from './export-pdf';

type FieldKey = 'start' | 'end' | 'stop';
type MapboxMap = any;
type Marker = any;

@Component({
  selector: 'app-root',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './app.html',
  styleUrl: './app.scss',
})
export class App implements OnInit, OnDestroy {
  private readonly api = inject(ApiService);
  private readonly destroyRef = inject(DestroyRef);
  private readonly zone = inject(NgZone);
  private readonly cdr = inject(ChangeDetectorRef);

  map?: MapboxMap;
  profile = 'mapbox/driving';
  startQuery = '';
  endQuery = '';
  stopQuery = '';
  start?: Place;
  end?: Place;
  stops: Place[] = [];
  suggestions: SearchHit[] = [];
  activeField: FieldKey = 'start';
  loading = false;
  searching = false;
  exporting = false;
  error = '';
  result?: PlanResponse;
  mapReady = false;
  focusedPlaceKey = '';
  searchSession = crypto.randomUUID();
  pendingPick?: Place | null = null;
  /** Mobile bottom sheet: open for planning, collapse to focus the map. */
  mobileSheetOpen = true;

  private markers: Marker[] = [];
  private readonly search$ = new Subject<{ field: FieldKey; q: string }>();

  readonly profiles = [
    { id: 'mapbox/driving', label: 'Driving' },
    { id: 'mapbox/driving-traffic', label: 'Driving + traffic' },
    { id: 'mapbox/walking', label: 'Walking' },
    { id: 'mapbox/cycling', label: 'Cycling' },
  ];

  ngOnInit(): void {
    this.search$
      .pipe(
        debounceTime(280),
        distinctUntilChanged((a, b) => a.field === b.field && a.q === b.q),
        filter((s) => s.q.trim().length >= 2),
        switchMap((s) => {
          this.searching = true;
          const c = this.map?.getCenter?.();
          const proximity = c ? `${c.lng},${c.lat}` : undefined;
          return this.api.search(s.q.trim(), { session: this.searchSession, proximity });
        }),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe({
        next: (hits) => {
          this.searching = false;
          this.suggestions = hits;
          this.scrollActiveFieldIntoView();
        },
        error: () => {
          this.searching = false;
          this.suggestions = [];
        },
      });

    this.api.config().subscribe({
      next: (cfg) => this.initMap(cfg.mapboxToken),
      error: () =>
        (this.error =
          'Could not load Mapbox config. Locally: start the Go API on :8080. On Netlify: set API_BASE_URL to your Railway URL and redeploy.'),
    });
  }

  ngOnDestroy(): void {
    this.clearMarkers();
    this.map?.remove();
  }

  focusField(field: FieldKey): void {
    this.activeField = field;
    if (!this.mobileSheetOpen && this.isMobileLayout()) {
      this.setMobileSheet(true);
    }
  }

  toggleMobileSheet(): void {
    this.setMobileSheet(!this.mobileSheetOpen);
  }

  private setMobileSheet(open: boolean): void {
    this.mobileSheetOpen = open;
    // Mapbox needs a resize after the sheet changes the map viewport.
    queueMicrotask(() => this.map?.resize());
    setTimeout(() => this.map?.resize(), 320);
  }

  private isMobileLayout(): boolean {
    return typeof window !== 'undefined' && window.matchMedia('(max-width: 840px)').matches;
  }

  /** Keep the focused search field + suggestions above the mobile keyboard. */
  private scrollActiveFieldIntoView(): void {
    if (!this.isMobileLayout()) return;
    queueMicrotask(() => {
      const field = document.querySelector('.field.focused') as HTMLElement | null;
      field?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
      const suggest = field?.querySelector('.suggest') as HTMLElement | null;
      suggest?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
    });
  }

  onQuery(field: FieldKey, q: string): void {
    this.activeField = field;
    if (q.trim().length < 2) {
      this.suggestions = [];
      return;
    }
    this.search$.next({ field, q });
  }

  chooseSuggestion(hit: SearchHit): void {
    const apply = (resolved: SearchHit) => {
      const place: Place = {
        name: resolved.name,
        address: resolved.address,
        lng: resolved.lng,
        lat: resolved.lat,
      };
      this.assignPlace(this.activeField, place);
      if (this.activeField === 'stop') {
        this.stopQuery = '';
      } else if (this.activeField === 'start') {
        this.startQuery = resolved.name;
      } else if (this.activeField === 'end') {
        this.endQuery = resolved.name;
      }
      this.suggestions = [];
      this.searchSession = crypto.randomUUID();
      this.redraw();
      this.flyTo(place, true);
    };

    if (hit.id && !(hit.lng || hit.lat)) {
      this.searching = true;
      this.api.retrieve(hit.id, this.searchSession).subscribe({
        next: (resolved) => {
          this.searching = false;
          apply(resolved);
        },
        error: () => {
          this.searching = false;
          this.error = 'Could not load that place. Try another suggestion.';
        },
      });
      return;
    }
    apply(hit);
  }

  typeLabel(type?: string): string {
    switch (type) {
      case 'poi':
        return 'Place';
      case 'address':
        return 'Address';
      case 'street':
        return 'Street';
      case 'place':
      case 'city':
        return 'City';
      case 'locality':
      case 'neighborhood':
        return 'Area';
      case 'region':
        return 'Region';
      case 'country':
        return 'Country';
      default:
        return type ? type : 'Result';
    }
  }

  useStartAsEnd(): void {
    if (!this.start) return;
    this.end = { ...this.start };
    this.endQuery = this.startQuery || this.start.name;
    this.result = undefined;
    this.redraw();
  }

  removeStop(index: number): void {
    this.stops = this.stops.filter((_, i) => i !== index);
    this.result = undefined;
    this.redraw();
  }

  clearAll(): void {
    this.start = undefined;
    this.end = undefined;
    this.stops = [];
    this.startQuery = '';
    this.endQuery = '';
    this.stopQuery = '';
    this.result = undefined;
    this.error = '';
    this.activeField = 'start';
    this.suggestions = [];
    this.focusedPlaceKey = '';
    this.pendingPick = null;
    if (this.isMobileLayout()) {
      this.setMobileSheet(true);
    }
    this.redraw();
  }

  plan(): void {
    if (!this.start || !this.end) {
      this.error = 'Set both a start and an end location.';
      return;
    }
    if (this.stops.length > 10) {
      this.error = 'Mapbox Optimization allows at most 10 stops between start and end.';
      return;
    }
    this.loading = true;
    this.error = '';
    this.api
      .plan({
        profile: this.profile,
        start: this.start,
        end: this.end,
        places: this.stops,
      })
      .subscribe({
        next: (res) => {
          this.result = res;
          this.loading = false;
          this.drawRoute(res);
          this.placeOptimizedMarkers(res.ordered);
          if (this.isMobileLayout()) {
            this.setMobileSheet(false);
          }
        },
        error: (err: HttpErrorResponse) => {
          this.loading = false;
          const body = err.error;
          this.error =
            (typeof body === 'string' ? body : body?.error) || err.message || 'Could not build a route.';
        },
      });
  }

  async exportPdf(): Promise<void> {
    if (!this.result || this.exporting) return;
    this.exporting = true;
    this.error = '';
    try {
      await exportRoutePdf(this.result, {
        duration: this.durationLabel(),
        distance: this.distanceLabel(),
        role: (i, n) => this.placeRole(i, n),
      });
    } catch {
      this.error = 'Could not export the PDF. Try again.';
    } finally {
      this.exporting = false;
    }
  }

  durationLabel(seconds = this.result?.durationS): string {
    if (seconds == null) return '';
    const s = Math.round(seconds);
    const h = Math.floor(s / 3600);
    const m = Math.round((s % 3600) / 60);
    if (h > 0) return `${h}h ${m}m`;
    if (m > 0) return `${m} min`;
    return '<1 min';
  }

  distanceLabel(meters = this.result?.distanceM): string {
    if (meters == null) return '';
    const km = meters / 1000;
    if (km >= 100) return `${Math.round(km)} km`;
    if (km >= 1) return `${km.toFixed(1)} km`;
    return `${Math.round(meters)} m`;
  }

  remainingStops(): number {
    return Math.max(0, 10 - this.stops.length);
  }

  placeRole(index: number, total: number): string {
    if (index === 0) return 'Start';
    if (index === total - 1) return 'End';
    return `Stop ${index}`;
  }

  placeKey(place: Place, index = -1): string {
    return `${index}:${place.lng.toFixed(5)},${place.lat.toFixed(5)}:${place.name}`;
  }

  focusPlace(place: Place, index = -1): void {
    this.focusedPlaceKey = this.placeKey(place, index);
    if (this.isMobileLayout()) {
      this.setMobileSheet(false);
    }
    this.flyTo(place, true);
  }

  isFocused(place: Place, index = -1): boolean {
    return this.focusedPlaceKey === this.placeKey(place, index);
  }

  private initMap(token: string): void {
    mapboxgl.accessToken = token;
    this.map = new mapboxgl.Map({
      container: 'map',
      style: 'mapbox://styles/mapbox/streets-v12',
      center: [12.5, 41.9],
      zoom: 2.1,
      projection: 'globe',
    });
    this.map.addControl(new mapboxgl.NavigationControl({ visualizePitch: true }), 'bottom-right');
    this.map.on('load', () => {
      this.mapReady = true;
      this.map?.setFog({
        color: 'rgb(186, 210, 235)',
        'high-color': 'rgb(36, 92, 223)',
        'horizon-blend': 0.02,
        'space-color': 'rgb(11, 11, 25)',
        'star-intensity': 0.6,
      });
    });
    this.map.on('click', (e: { lngLat: { lng: number; lat: number } }) => {
      const { lng, lat } = e.lngLat;
      // Mapbox events run outside Angular; re-enter so the confirm dialog renders (esp. on mobile).
      this.zone.run(() => {
        this.api.reverse(lng, lat).subscribe({
          next: (hit) =>
            this.handleMapPick({ name: hit.name, address: hit.address, lng: hit.lng, lat: hit.lat }),
          error: () =>
            this.handleMapPick({
              name: `${lat.toFixed(4)}, ${lng.toFixed(4)}`,
              address: 'Dropped pin',
              lng,
              lat,
            }),
        });
      });
    });
  }

  /** Desktop: confirm once the trip is filled or a route exists. Mobile: always confirm map taps. */
  private shouldConfirmMapPick(): boolean {
    if (this.isMobileLayout()) return true;
    return !!this.result || (!!this.start && !!this.end);
  }

  private handleMapPick(place: Place): void {
    if (this.shouldConfirmMapPick()) {
      this.pendingPick = place;
      this.suggestions = [];
      if (this.isMobileLayout() && this.mobileSheetOpen) {
        this.setMobileSheet(false);
      }
      this.cdr.detectChanges();
      return;
    }
    this.applyPicked(place);
    this.cdr.detectChanges();
  }

  confirmPick(as: FieldKey): void {
    if (!this.pendingPick) return;
    const place = this.pendingPick;
    this.pendingPick = null;
    this.assignPlace(as, place);
    this.result = undefined;
    this.activeField = as;
    this.redraw();
    this.flyTo(place, true);
  }

  cancelPick(): void {
    this.pendingPick = null;
  }

  private applyPicked(place: Place): void {
    const target = this.mapClickTarget();
    this.assignPlace(target, place);
    this.result = undefined;
    this.redraw();
  }

  private mapClickTarget(): FieldKey {
    if (this.activeField === 'start' || this.activeField === 'end' || this.activeField === 'stop') {
      if (this.activeField === 'stop' && this.stops.length >= 10) return 'end';
      return this.activeField;
    }
    if (!this.start) return 'start';
    if (!this.end) return 'end';
    return 'stop';
  }

  private assignPlace(field: FieldKey, place: Place): void {
    if (field === 'start') {
      this.start = place;
      this.startQuery = place.name;
      return;
    }
    if (field === 'end') {
      this.end = place;
      this.endQuery = place.name;
      return;
    }
    if (this.stops.length >= 10) {
      this.error = 'Maximum of 10 stops on a Mapbox-optimized trip.';
      return;
    }
    this.addStop(place);
  }

  private addStop(place: Place): void {
    if (this.stops.length >= 10) return;
    this.stops = [...this.stops, place];
  }

  private flyTo(place: Place, spotlight = false): void {
    const zoom = spotlight ? Math.max(this.map?.getZoom?.() ?? 2, 11) : Math.max(this.map?.getZoom?.() ?? 2, 8);
    this.map?.flyTo({
      center: [place.lng, place.lat],
      zoom,
      speed: spotlight ? 1.15 : 1.4,
      curve: 1.4,
      essential: true,
    });
  }

  private redraw(): void {
    this.clearMarkers();
    if (!this.map) return;

    const sameEnds =
      !!this.start &&
      !!this.end &&
      Math.abs(this.start.lng - this.end.lng) < 1e-5 &&
      Math.abs(this.start.lat - this.end.lat) < 1e-5;

    if (this.start) {
      if (sameEnds) {
        this.markers.push(
          this.makeMarker(this.start, 'A/B', 'both', {
            role: 'Start & End',
            name: this.start.name,
            address: this.start.address,
          }),
        );
      } else {
        this.markers.push(
          this.makeMarker(this.start, 'A', 'start', {
            role: 'Start',
            name: this.start.name,
            address: this.start.address,
          }),
        );
      }
    }
    this.stops.forEach((s, i) =>
      this.markers.push(
        this.makeMarker(s, String(i + 1), 'stop', {
          role: `Stop ${i + 1}`,
          name: s.name,
          address: s.address,
        }),
      ),
    );
    if (this.end && !sameEnds) {
      this.markers.push(
        this.makeMarker(this.end, 'B', 'end', {
          role: 'End',
          name: this.end.name,
          address: this.end.address,
        }),
      );
    }
    this.removeRoute();
  }

  /** Pins follow visit order: A (start), 1…n (stops), B (end). Same start/end → split green/red. */
  private placeOptimizedMarkers(ordered: Place[]): void {
    this.clearMarkers();
    if (!this.map || !ordered.length) return;

    const last = ordered.length - 1;
    const roundTrip =
      ordered.length > 1 &&
      Math.abs(ordered[0].lng - ordered[last].lng) < 1e-5 &&
      Math.abs(ordered[0].lat - ordered[last].lat) < 1e-5;

    ordered.forEach((place, i) => {
      if (roundTrip && i === last) return;

      let kind = 'stop';
      let label = String(i);
      let role = `Stop ${i}`;

      if (i === 0) {
        if (roundTrip) {
          kind = 'both';
          label = 'A/B';
          role = 'Start & End';
        } else {
          kind = 'start';
          label = 'A';
          role = 'Start';
        }
      } else if (i === last) {
        kind = 'end';
        label = 'B';
        role = 'End';
      }

      this.markers.push(
        this.makeMarker(place, label, kind, {
          role,
          name: place.name,
          address: place.address,
        }),
      );
    });
  }

  private makeMarker(
    place: Place,
    label: string,
    kind: string,
    tip: { role: string; name: string; address?: string },
  ): Marker {
    const el = document.createElement('div');
    el.className = `pin pin-${kind}`;
    const labelEl = document.createElement('span');
    labelEl.textContent = label;
    el.appendChild(labelEl);

    const popup = new mapboxgl.Popup({
      offset: 28,
      closeButton: false,
      closeOnClick: false,
      maxWidth: '320px',
      className: `pin-tip pin-tip-${kind}`,
    }).setHTML(this.tooltipHtml(tip, place));

    const marker = new mapboxgl.Marker({ element: el }).setLngLat([place.lng, place.lat]).setPopup(popup).addTo(this.map!);

    el.addEventListener('mouseenter', () => {
      const p = marker.getPopup();
      if (p && !p.isOpen()) marker.togglePopup();
    });
    el.addEventListener('mouseleave', () => {
      const p = marker.getPopup();
      if (p?.isOpen()) marker.togglePopup();
    });

    return marker;
  }

  private tooltipHtml(tip: { role: string; name: string; address?: string }, place: Place): string {
    const addr = tip.address?.trim()
      ? `<p class="pin-tip-addr">${this.escapeHtml(tip.address)}</p>`
      : '';
    const coords = `${place.lat.toFixed(4)}, ${place.lng.toFixed(4)}`;
    return `
      <div class="pin-tip-card">
        <span class="pin-tip-role">${this.escapeHtml(tip.role)}</span>
        <strong class="pin-tip-name">${this.escapeHtml(tip.name)}</strong>
        ${addr}
        <p class="pin-tip-meta">${coords}</p>
      </div>
    `;
  }

  private escapeHtml(value: string): string {
    return value
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  private drawRoute(res: PlanResponse): void {
    if (!this.map) return;
    this.removeRoute();
    const source = {
      type: 'geojson' as const,
      data: { type: 'Feature' as const, properties: {}, geometry: res.geometry },
    };
    if (this.map.getSource('route')) {
      this.map.getSource('route').setData(source.data);
    } else {
      this.map.addSource('route', source);
      this.map.addLayer({
        id: 'route-glow',
        type: 'line',
        source: 'route',
        paint: { 'line-color': '#4285F4', 'line-width': 10, 'line-opacity': 0.25 },
      });
      this.map.addLayer({
        id: 'route-line',
        type: 'line',
        source: 'route',
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: { 'line-color': '#1A73E8', 'line-width': 4 },
      });
    }
    const coords = res.geometry.coordinates;
    if (coords?.length) {
      const bounds = coords.reduce(
        (b, c) => b.extend(c as [number, number]),
        new mapboxgl.LngLatBounds(coords[0] as [number, number], coords[0] as [number, number]),
      );
      this.map.fitBounds(bounds, { padding: 80, maxZoom: 12, duration: 900 });
    }
  }

  private removeRoute(): void {
    if (!this.map?.getLayer('route-line')) return;
    this.map.removeLayer('route-line');
    this.map.removeLayer('route-glow');
    this.map.removeSource('route');
  }

  private clearMarkers(): void {
    this.markers.forEach((m) => m.remove());
    this.markers = [];
  }
}
