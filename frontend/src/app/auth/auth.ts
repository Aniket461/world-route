import { Injectable, computed, inject, signal } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { Router } from '@angular/router';
import { Observable, tap, catchError, of } from 'rxjs';
import { environment } from '../../environments/environment';

export interface AuthUser {
  id: string;
  username: string;
  email: string;
  displayName: string;
  createdAt?: string;
}

interface AuthResponse {
  token: string;
  user: AuthUser;
}

const TOKEN_KEY = 'wr_token';
const USER_KEY = 'wr_user';

@Injectable({ providedIn: 'root' })
export class AuthService {
  private readonly http = inject(HttpClient);
  private readonly router = inject(Router);
  private readonly base = (environment.apiBaseUrl || '').trim().replace(/^=+/, '').replace(/\/$/, '');

  private readonly userSignal = signal<AuthUser | null>(this.readStoredUser());
  readonly user = this.userSignal.asReadonly();
  readonly isLoggedIn = computed(() => !!this.userSignal());

  token(): string | null {
    return localStorage.getItem(TOKEN_KEY);
  }

  private url(path: string): string {
    return `${this.base}${path}`;
  }

  private readStoredUser(): AuthUser | null {
    try {
      const raw = localStorage.getItem(USER_KEY);
      return raw ? (JSON.parse(raw) as AuthUser) : null;
    } catch {
      return null;
    }
  }

  private persist(token: string, user: AuthUser): void {
    localStorage.setItem(TOKEN_KEY, token);
    localStorage.setItem(USER_KEY, JSON.stringify(user));
    this.userSignal.set(user);
  }

  clearSession(): void {
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem(USER_KEY);
    this.userSignal.set(null);
  }

  register(body: {
    username: string;
    email: string;
    displayName: string;
    password: string;
  }): Observable<AuthResponse> {
    return this.http.post<AuthResponse>(this.url('/api/auth/register'), body).pipe(
      tap((res) => this.persist(res.token, res.user)),
    );
  }

  login(body: { login: string; password: string }): Observable<AuthResponse> {
    return this.http.post<AuthResponse>(this.url('/api/auth/login'), body).pipe(
      tap((res) => this.persist(res.token, res.user)),
    );
  }

  refreshMe(): Observable<AuthUser | null> {
    if (!this.token()) {
      this.clearSession();
      return of(null);
    }
    return this.http.get<AuthUser>(this.url('/api/auth/me')).pipe(
      tap((user) => {
        localStorage.setItem(USER_KEY, JSON.stringify(user));
        this.userSignal.set(user);
      }),
      catchError(() => {
        this.clearSession();
        return of(null);
      }),
    );
  }

  logout(): void {
    this.clearSession();
    void this.router.navigateByUrl('/login');
  }

  initial(): string {
    const u = this.userSignal();
    const name = u?.displayName || u?.username || '?';
    return name.trim().charAt(0).toUpperCase() || '?';
  }
}
