import { Component, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { AuthService } from './auth';

@Component({
  selector: 'app-login',
  standalone: true,
  imports: [CommonModule, FormsModule, RouterLink],
  templateUrl: './login.html',
  styleUrl: './login.scss',
})
export class LoginComponent {
  private readonly auth = inject(AuthService);
  private readonly router = inject(Router);
  private readonly route = inject(ActivatedRoute);

  login = '';
  password = '';
  error = '';
  loading = false;

  submit(): void {
    this.error = '';
    this.loading = true;
    this.auth.login({ login: this.login.trim(), password: this.password }).subscribe({
      next: () => {
        this.loading = false;
        const next = this.route.snapshot.queryParamMap.get('next') || '/';
        void this.router.navigateByUrl(next);
      },
      error: (err) => {
        this.loading = false;
        this.error = err?.error?.error || 'Could not sign in.';
      },
    });
  }
}
