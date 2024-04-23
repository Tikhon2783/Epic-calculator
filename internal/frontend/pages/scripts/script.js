function submitRegForm(event) {
    event.preventDefault();

    console.log('sending register request...');
    
    const username = document.getElementById('username').value;
    const password = document.getElementById('password').value;
    
    if (!username || !password) {
        alert('Please fill in all fields');
        return;
    }
    
    const form = document.getElementById('authForm');
    const formData = new URLSearchParams();
    formData.append('username', username);
    formData.append('password', password);

    fetch('/calculator/internal/register', {
        method: 'POST',
        body: formData
    })
    .then(response => {
        if(response.ok) {
            console.log('Request successful');
            alert('Успешно зарегстрировались и зашли в аккаунт =D');
            sessionStorage.setItem("loggedInUser", username)
        } else {
            console.error('Request failed');
            alert('Ошибка: code', response.status);
        }
    })
    .catch(error => console.error('Error:', error));
}


document.addEventListener('DOMContentLoaded', function() {
    const loggedInUser = sessionStorage.getItem('loggedInUser');
    const profileUsername = document.querySelector('.profile-username-btn');
    const logoutButton = document.querySelector('.logout-btn');
    console.log("page loaded", loggedInUser)

    document.querySelector("#authForm").addEventListener("click", function () {
        logoutButton.style.display = "none";
    });

    if (loggedInUser) {
        // User is logged in
        profileUsername.textContent = loggedInUser;

        profileUsername.addEventListener('click', function() {
            logoutButton.style.display = 'block';
            
            logoutButton.addEventListener('click', function() {
                // Perform logout functionality here (e.g., clearing session, redirecting, etc.)
                sessionStorage.removeItem('loggedInUser');
                logoutButton.style.display = 'none';
                profileUsername.textContent = 'Log in';
                profileUsername.addEventListener('click', function() {
                    location.replace('file:///C:/Users/tikho/Documents/Coding/web-projects/authentification-tests/login-page/index.html')
                });
                alert('Logged out.');
            });
        });
    } else {
        // Simulate login process (you should replace this with actual login functionality)
        profileUsername.textContent = "Sign In";
    
        profileUsername.addEventListener('click', function() {
            location.replace('file:///C:/Users/tikho/Documents/Coding/web-projects/authentification-tests/login-page/index.html')
        });
    }
});
