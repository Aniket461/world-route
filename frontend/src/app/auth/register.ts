import { Component, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { Router, RouterLink } from '@angular/router';
import { AuthService } from './auth';

@Component({
  selector: 'app-register',
  standalone: true,
  imports: [CommonModule, FormsModule, RouterLink],
  templateUrl: './register.html',
  styleUrl: './login.scss',
})
export class RegisterComponent {
  private readonly auth = inject(AuthService);
  private readonly router = inject(Router);

  username = '';
  email = '';
  displayName = '';
  password = '';
  error = '';
  loading = false;

  submit(): void {
    this.error = '';
    this.loading = true;
    this.auth
      .register({
        username: this.username.trim(),
        email: this.email.trim(),
        displayName: this.displayName.trim() || this.username.trim(),
        password: this.password,
      })
      .subscribe({
        next: () => {
          this.loading = false;
          void this.router.navigateByUrl('/user');
        },
        error: (err) => {
          this.loading = false;
          this.error = err?.error?.error || 'Could not create account.';
        },
      });
  }
}
