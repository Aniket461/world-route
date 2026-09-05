import { Routes } from '@angular/router';
import { authGuard } from './auth/auth.guard';
import { LoginComponent } from './auth/login';
import { RegisterComponent } from './auth/register';
import { PlannerComponent } from './planner/planner';
import { UserComponent } from './user/user';

export const routes: Routes = [
  { path: '', component: PlannerComponent },
  { path: 'login', component: LoginComponent },
  { path: 'register', component: RegisterComponent },
  { path: 'user', component: UserComponent, canActivate: [authGuard] },
  { path: '**', redirectTo: '' },
];
