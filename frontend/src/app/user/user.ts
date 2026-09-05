import { Component, OnInit, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { Router, RouterLink } from '@angular/router';
import { AuthService } from '../auth/auth';
import { ApiService, SavedTripSummary } from '../api';

@Component({
  selector: 'app-user',
  standalone: true,
  imports: [CommonModule, RouterLink],
  templateUrl: './user.html',
  styleUrl: './user.scss',
})
export class UserComponent implements OnInit {
  private readonly auth = inject(AuthService);
  private readonly api = inject(ApiService);
  private readonly router = inject(Router);

  readonly user = this.auth.user;
  trips: SavedTripSummary[] = [];
  loading = true;
  error = '';

  ngOnInit(): void {
    this.auth.refreshMe().subscribe();
    this.reload();
  }

  reload(): void {
    this.loading = true;
    this.error = '';
    this.api.listTrips().subscribe({
      next: (trips) => {
        this.trips = trips;
        this.loading = false;
      },
      error: (err) => {
        this.loading = false;
        this.error = err?.error?.error || 'Could not load saved trips.';
      },
    });
  }

  openTrip(id: string): void {
    void this.router.navigate(['/'], { queryParams: { trip: id } });
  }

  deleteTrip(id: string, event: Event): void {
    event.stopPropagation();
    if (!confirm('Delete this saved trip?')) return;
    this.api.deleteTrip(id).subscribe({
      next: () => {
        this.trips = this.trips.filter((t) => t.id !== id);
      },
      error: () => {
        this.error = 'Could not delete trip.';
      },
    });
  }

  logout(): void {
    this.auth.logout();
  }

  initial(): string {
    return this.auth.initial();
  }

  durationLabel(seconds: number): string {
    const s = Math.round(seconds || 0);
    const h = Math.floor(s / 3600);
    const m = Math.round((s % 3600) / 60);
    if (h > 0) return `${h}h ${m}m`;
    if (m > 0) return `${m} min`;
    return '<1 min';
  }

  distanceLabel(meters: number): string {
    const km = (meters || 0) / 1000;
    if (km >= 100) return `${Math.round(km)} km`;
    if (km >= 1) return `${km.toFixed(1)} km`;
    return `${Math.round(meters || 0)} m`;
  }
}
