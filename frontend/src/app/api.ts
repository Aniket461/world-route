import { Injectable } from '@angular/core';
import { HttpClient, HttpParams } from '@angular/common/http';
import { Observable } from 'rxjs';
import { environment } from '../environments/environment';

export interface Place {
  name: string;
  address?: string;
  lng: number;
  lat: number;
}

export interface SearchHit {
  id?: string;
  name: string;
  address: string;
  type?: string;
  lng: number;
  lat: number;
}

export interface RouteLeg {
  from: string;
  to: string;
  distanceM: number;
  durationS: number;
}

export interface PlanResponse {
  ordered: Place[];
  legs: RouteLeg[];
  distanceM: number;
  durationS: number;
  geometry: { type: 'LineString'; coordinates: number[][] };
  waypointCount: number;
  profile: string;
  warning?: string;
}

@Injectable({ providedIn: 'root' })
export class ApiService {
  private readonly base = (environment.apiBaseUrl || '')
    .trim()
    .replace(/^=+/, '')
    .replace(/\/$/, '');

  constructor(private readonly http: HttpClient) {}

  private url(path: string): string {
    return `${this.base}${path}`;
  }

  config(): Observable<{ mapboxToken: string }> {
    return this.http.get<{ mapboxToken: string }>(this.url('/api/config'));
  }

  search(
    q: string,
    opts: { session: string; proximity?: string },
  ): Observable<SearchHit[]> {
    let params = new HttpParams().set('q', q).set('session', opts.session);
    if (opts.proximity) {
      params = params.set('proximity', opts.proximity);
    }
    return this.http.get<SearchHit[]>(this.url('/api/search'), { params });
  }

  retrieve(id: string, session: string): Observable<SearchHit> {
    return this.http.get<SearchHit>(this.url('/api/search/retrieve'), {
      params: new HttpParams().set('id', id).set('session', session),
    });
  }

  reverse(lng: number, lat: number): Observable<SearchHit> {
    return this.http.get<SearchHit>(this.url('/api/reverse'), {
      params: new HttpParams().set('lng', String(lng)).set('lat', String(lat)),
    });
  }

  plan(body: { profile: string; start: Place; end: Place; places: Place[] }): Observable<PlanResponse> {
    return this.http.post<PlanResponse>(this.url('/api/plan'), body);
  }
}
